package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/config"
	"github.com/pgsty/piglet/internal/diagnostics"
	"github.com/pgsty/piglet/internal/doctor"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/hostconfig"
	"github.com/pgsty/piglet/internal/image"
	"github.com/pgsty/piglet/internal/lease"
	darwinnet "github.com/pgsty/piglet/internal/network/darwin"
	linuxnet "github.com/pgsty/piglet/internal/network/linux"
	netpreflight "github.com/pgsty/piglet/internal/network/preflight"
	"github.com/pgsty/piglet/internal/network/subnet"
	pigstyint "github.com/pgsty/piglet/internal/pigsty"
	privatevm "github.com/pgsty/piglet/internal/private"
	"github.com/pgsty/piglet/internal/profile"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/quick"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/sshconfig"
	"github.com/pgsty/piglet/internal/state"
	"github.com/pgsty/piglet/internal/version"
	"github.com/pgsty/piglet/internal/vm"
	"golang.org/x/term"
)

const (
	exitOK         = 0
	exitRuntime    = 1
	exitUsage      = 2
	exitCapability = 3
	exitConflict   = 4
	exitPartial    = 5
	exitLease      = 6
	exitIntegrity  = 7
)

type repeatedForward []string

func (f *repeatedForward) String() string { return strings.Join(*f, ",") }
func (f *repeatedForward) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func configurationWarnings(resolved spec.Resolved) []string {
	warnings := make([]string, 0)
	if resolved.Network == "private" && resolved.Private != nil {
		if layout, err := subnet.Parse(resolved.Private.CIDR); err == nil {
			if warning := layout.Warning(); warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}
	for _, node := range resolved.Nodes {
		for _, forward := range node.Forwards {
			address := net.ParseIP(forward.Bind)
			if address != nil && !address.IsLoopback() {
				warnings = append(warnings, fmt.Sprintf("WARNING: node %s exposes host TCP %s:%d beyond loopback; this may make guest port %d reachable from other machines", node.Name, forward.Bind, forward.Host, forward.Guest))
			}
		}
	}
	return warnings
}

func printWarnings(out io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintln(out, warning)
	}
}

func printImageStatusWarning(out io.Writer, entry image.Entry) {
	if entry.Status != "" && entry.Status != "supported" {
		fmt.Fprintf(out, "WARNING: image %s/%s (%s) has status %s, not supported; use only with the corresponding test/risk acceptance\n", entry.Alias, entry.Arch, entry.Release, entry.Status)
	}
}

func confirmDestructive(force, interactive bool, action string, input io.Reader, output io.Writer) error {
	if force {
		return nil
	}
	if !interactive {
		return fmt.Errorf("%s requires --force when stdin is not a TTY", action)
	}
	fmt.Fprintf(output, "Confirm scoped Piglet %s by typing %q: ", action, action)
	line, err := bufio.NewReader(io.LimitReader(input, 256)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read %s confirmation: %w", action, err)
	}
	if strings.TrimSpace(line) != action {
		return fmt.Errorf("%s confirmation did not match %q", action, action)
	}
	return nil
}

func confirmCLIAction(force bool, action string, stderr io.Writer) error {
	return confirmDestructive(force, term.IsTerminal(int(os.Stdin.Fd())), action, os.Stdin, stderr)
}

func withReadinessTimeout(base time.Duration, resolved spec.Resolved) time.Duration {
	wait, err := resolved.SSHWaitTimeout()
	if err != nil || wait > time.Duration(1<<63-1)-5*time.Minute {
		return base
	}
	if candidate := wait + 5*time.Minute; candidate > base {
		return candidate
	}
	return base
}

func loadPrivatePreflightConfig(path string) (spec.Resolved, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return spec.Resolved{}, err
	}
	file, err := config.Load(absolute)
	if err != nil {
		return spec.Resolved{}, err
	}
	resolved, err := file.Resolve()
	if err != nil {
		return spec.Resolved{}, err
	}
	if resolved.Network != "private" || resolved.Private == nil {
		return spec.Resolved{}, errors.New("network preflight -f requires a valid private configuration")
	}
	return resolved, nil
}

func usage(out io.Writer) {
	fmt.Fprintln(out, "usage: piglet <command> [options]")
	fmt.Fprintln(out, "commands: init, validate, plan, doctor, up, start, stop, restart, recreate, status, list, ssh, exec, ssh-config, hosts, logs, repair, destroy, image, project, network, pigsty, debug, completion, version")
}

type sudoRunner struct{ base execx.Runner }

func (r sudoRunner) Run(ctx context.Context, binary string, args ...string) (execx.Result, error) {
	if r.base == nil {
		return execx.Result{}, errors.New("sudo runner has no base runner")
	}
	sudoArgs := append([]string{"-n", "--", binary}, args...)
	return r.base.Run(ctx, "/usr/bin/sudo", sudoArgs...)
}

type darwinNetworkInstaller interface {
	InstallModeNetwork(context.Context, string, string, string, string, string, bool) (darwinnet.InstallReport, error)
}

func installDarwinNetwork(ctx context.Context, installer darwinNetworkInstaller, archive, interfaceID, arch, mode, cidr string, apply bool) (darwinnet.InstallReport, error) {
	return installer.InstallModeNetwork(ctx, archive, interfaceID, arch, mode, cidr, apply)
}

func runNetwork(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: piglet network preflight|status|install|uninstall [--json] [--yes]")
		return exitUsage
	}
	command := args[0]
	if command == "preflight" {
		flags := flag.NewFlagSet("network preflight", flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		networkCIDR := flags.String("cidr", "", "candidate host-global canonical RFC1918 IPv4 /24; defaults to 10.10.10.0/24")
		configPath := flags.String("f", "", "private configuration whose exact node addresses should be probed")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			fmt.Fprintln(stderr, "network preflight supports native Linux and Darwin")
			return exitCapability
		}
		cidr := *networkCIDR
		if cidr == "" {
			cidr = subnet.DefaultCIDR
		}
		layout, err := subnet.Parse(cidr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		addresses := layout.StaticAddresses()
		if *configPath != "" {
			resolved, loadErr := loadPrivatePreflightConfig(*configPath)
			if loadErr != nil {
				fmt.Fprintln(stderr, loadErr)
				return exitUsage
			}
			configLayout, layoutErr := subnet.Parse(resolved.Private.CIDR)
			if layoutErr != nil {
				fmt.Fprintln(stderr, layoutErr)
				return exitUsage
			}
			if *networkCIDR != "" && configLayout.CIDR() != layout.CIDR() {
				fmt.Fprintln(stderr, "--cidr differs from the private configuration; generate or edit one coordinated config instead of overriding it during preflight")
				return exitUsage
			}
			layout = configLayout
			addresses = addresses[:0]
			for _, node := range resolved.Nodes {
				addresses = append(addresses, node.Address)
			}
		}
		if leaseStatus, leaseErr := (lease.Store{}).Inspect(); leaseErr == nil && leaseStatus.Active {
			addresses = nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		report := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Inspect, Layout: layout, Addresses: addresses}, netpreflight.Probe{Runner: execx.OSRunner{Timeout: 5 * time.Second, OutputLimit: 1 << 20}})
		if *jsonOutput {
			if code := encodeJSON(stdout, stderr, report); code != exitOK {
				return code
			}
		} else {
			fmt.Fprintf(stdout, "network: %s host=%s dhcp_end=%s ready=%t\n", report.CIDR, report.HostAddress, report.DHCPEnd, report.Ready)
			fmt.Fprintf(stdout, "installation: %s", report.Installation.Status)
			if report.Installation.Mode != "" {
				fmt.Fprintf(stdout, " mode=%s cidr=%s interface=%s healthy=%t", report.Installation.Mode, report.Installation.CIDR, report.Installation.Interface, report.Installation.Healthy)
			}
			fmt.Fprintln(stdout)
			for _, finding := range report.Findings {
				fmt.Fprintf(stdout, "%s %s: %s\n", finding.Severity, finding.Code, finding.Evidence)
			}
		}
		return report.ExitCode
	}
	if command == "install" || command == "uninstall" {
		flags := flag.NewFlagSet("network "+command, flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		apply := flags.Bool("yes", false, "apply the displayed privileged plan")
		archive := flags.String("archive", "", "Darwin: absolute pinned socket_vmnet archive")
		interfaceID := flags.String("interface-id", "", "Darwin fresh install: persistent vmnet UUID")
		vmnetMode := flags.String("mode", "host", "Darwin vmnet mode: host or shared")
		networkCIDR := flags.String("cidr", subnet.DefaultCIDR, "host-global canonical RFC1918 IPv4 /24")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			fmt.Fprintln(stderr, "product network install/uninstall supports native Linux and Darwin")
			return exitCapability
		}
		if runtime.GOOS == "linux" && *vmnetMode != "host" {
			fmt.Fprintln(stderr, "--mode is a Darwin socket_vmnet option; Linux uses piglet0")
			return exitUsage
		}
		layout, layoutErr := subnet.Parse(*networkCIDR)
		if layoutErr != nil {
			fmt.Fprintln(stderr, layoutErr)
			return exitUsage
		}
		if command == "uninstall" && !layout.IsDefault() {
			fmt.Fprintln(stderr, "--cidr is only valid with network install/preflight; uninstall reads the installed ownership manifest")
			return exitUsage
		}
		baseRunner := execx.OSRunner{Timeout: 2 * time.Minute, OutputLimit: 1 << 20}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if command == "install" {
			addresses := layout.StaticAddresses()
			if leaseStatus, leaseErr := (lease.Store{}).Inspect(); leaseErr == nil && leaseStatus.Active {
				addresses = nil
			}
			preflightReport := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Install, Layout: layout, Addresses: addresses}, netpreflight.Probe{Runner: baseRunner})
			if !preflightReport.Ready {
				if *jsonOutput {
					_ = encodeJSON(stdout, stderr, preflightReport)
				} else {
					for _, finding := range preflightReport.Findings {
						fmt.Fprintf(stderr, "%s %s: %s\n", finding.Severity, finding.Code, finding.Evidence)
					}
				}
				return preflightReport.ExitCode
			}
		}
		if runtime.GOOS == "darwin" {
			executor := darwinnet.Executor{User: baseRunner, Root: sudoRunner{base: baseRunner}}
			if command == "install" {
				report, err := installDarwinNetwork(ctx, executor, *archive, *interfaceID, runtime.GOARCH, *vmnetMode, layout.CIDR(), *apply)
				if err != nil {
					fmt.Fprintln(stderr, err)
					if errors.Is(err, darwinnet.ErrVMNetSharingBusy) {
						return exitLease
					}
					return exitIntegrity
				}
				if *jsonOutput {
					return encodeJSON(stdout, stderr, report)
				}
				for _, warning := range report.Warnings {
					fmt.Fprintln(stderr, warning)
				}
				fmt.Fprintf(stdout, "action: %s\napplied: %t\n", report.Action, report.Applied)
				for path, metadata := range report.Targets {
					fmt.Fprintf(stdout, "%s %s\n", path, metadata)
				}
				if !report.Applied && report.Action != "none" {
					fmt.Fprintln(stdout, "rerun with --yes using the same --archive and --interface-id after reviewing this plan and caching sudo credentials")
				}
				return exitOK
			}
			report, err := executor.Uninstall(ctx, *apply)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitIntegrity
			}
			if *jsonOutput {
				return encodeJSON(stdout, stderr, report)
			}
			for _, path := range report.RemoveFiles {
				fmt.Fprintf(stdout, "remove file %s\n", path)
			}
			for _, path := range report.RemoveDirs {
				fmt.Fprintf(stdout, "rmdir %s\n", path)
			}
			fmt.Fprintf(stdout, "applied: %t\n", report.Applied)
			return exitOK
		}
		if *archive != "" || *interfaceID != "" {
			fmt.Fprintln(stderr, "--archive and --interface-id are Darwin-only")
			return exitUsage
		}
		executor := linuxnet.Executor{User: baseRunner, Root: sudoRunner{base: baseRunner}}
		if command == "install" {
			linuxConfig, configErr := linuxnet.ConfigForCIDR(layout.CIDR())
			if configErr != nil {
				fmt.Fprintln(stderr, configErr)
				return exitUsage
			}
			report, err := executor.InstallConfig(ctx, linuxConfig, *apply)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitIntegrity
			}
			if *jsonOutput {
				return encodeJSON(stdout, stderr, report)
			}
			for _, warning := range report.Warnings {
				fmt.Fprintln(stderr, warning)
			}
			for _, directory := range report.Plan.Directories {
				fmt.Fprintf(stdout, "directory %s %s %s\n", directory.Path, directory.Owner, directory.Mode)
			}
			for _, file := range report.Plan.Files {
				fmt.Fprintf(stdout, "file %s %s %s\n", file.Path, file.Owner, file.Mode)
			}
			for _, phase := range report.Plan.Phases {
				fmt.Fprintf(stdout, "phase %s\n", phase.Name)
				for _, action := range phase.Commands {
					fmt.Fprintf(stdout, "  %s\n", execx.Display(action.Binary, action.Args...))
				}
			}
			fmt.Fprintf(stdout, "applied: %t\n", report.Applied)
			if !report.Applied {
				fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact plan and caching sudo credentials")
			}
			return exitOK
		}
		report, err := executor.Uninstall(ctx, *apply)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitIntegrity
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, report)
		}
		for _, path := range report.Plan.RemoveFiles {
			fmt.Fprintf(stdout, "remove file %s\n", path)
		}
		for _, directory := range report.Plan.RemoveDirectories {
			fmt.Fprintf(stdout, "rmdir %s\n", directory)
		}
		fmt.Fprintf(stdout, "applied: %t\n", report.Applied)
		if !report.Applied {
			fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact plan and caching sudo credentials")
		}
		return exitOK
	}
	if command != "status" {
		fmt.Fprintln(stderr, "usage: piglet network preflight|status|install|uninstall [--json] [--yes]")
		return exitUsage
	}
	flags := flag.NewFlagSet("network status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	networkCIDR := flags.String("cidr", "", "expected host-global RFC1918 IPv4 /24; defaults to installed network or 10.10.10.0/24")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cidr := *networkCIDR
	if cidr == "" {
		cidr = subnet.DefaultCIDR
	}
	layout, err := subnet.Parse(cidr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	baseRunner := execx.OSRunner{Timeout: 5 * time.Second, OutputLimit: 1 << 20}
	preflightReport := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Inspect, Layout: layout}, netpreflight.Probe{Runner: baseRunner})
	installedReadable := preflightReport.Installation.Status == "exact" || preflightReport.Installation.Status == "protected"
	if *networkCIDR == "" && installedReadable && preflightReport.Installation.CIDR != "" && preflightReport.Installation.CIDR != layout.CIDR() {
		installedLayout, installedErr := subnet.Parse(preflightReport.Installation.CIDR)
		if installedErr == nil {
			preflightReport = netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Inspect, Layout: installedLayout}, netpreflight.Probe{Runner: baseRunner})
		}
	}
	doctorReport := (doctor.Probe{}).Run(ctx)
	checks := make([]doctor.Check, 0, 4)
	hasError := !preflightReport.Ready
	for _, check := range doctorReport.Checks {
		if check.Name == "kvm" || check.Name == "linux-family" || check.Name == "linux-networkd" || check.Name == "bridge-helper" {
			checks = append(checks, check)
			if check.Status == doctor.Error {
				hasError = true
			}
		}
	}
	leaseStatus, err := (lease.Store{}).Inspect()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitIntegrity
	}
	result := struct {
		OS        string              `json:"os"`
		Arch      string              `json:"arch"`
		Preflight netpreflight.Report `json:"preflight"`
		Checks    []doctor.Check      `json:"checks,omitempty"`
		Lease     lease.Status        `json:"lease"`
	}{OS: doctorReport.OS, Arch: doctorReport.Arch, Preflight: preflightReport, Checks: checks, Lease: leaseStatus}
	if *jsonOutput {
		if code := encodeJSON(stdout, stderr, result); code != exitOK {
			return code
		}
	} else {
		fmt.Fprintf(stdout, "network: %s ready=%t installation=%s\n", preflightReport.CIDR, preflightReport.Ready, preflightReport.Installation.Status)
		for _, finding := range preflightReport.Findings {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", finding.Severity, finding.Code, finding.Evidence)
		}
		for _, check := range checks {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Evidence)
		}
		fmt.Fprintf(stdout, "lease root: %s available=%t active=%t\n", leaseStatus.Root, leaseStatus.Available, leaseStatus.Active)
		if leaseStatus.Lease != nil {
			fmt.Fprintf(stdout, "lease project=%s owner_uid=%d generation=%d\n", leaseStatus.Lease.ProjectID, leaseStatus.Lease.OwnerUID, leaseStatus.Lease.Generation)
		}
	}
	if hasError {
		if preflightReport.ExitCode != 0 {
			return preflightReport.ExitCode
		}
		return exitCapability
	}
	return exitOK
}

func runProject(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: piglet project purge-keys|upgrade-state [options]")
		return exitUsage
	}
	if args[0] == "upgrade-state" {
		flags := flag.NewFlagSet("project upgrade-state", flag.ContinueOnError)
		flags.SetOutput(stderr)
		dryRun := flags.Bool("dry-run", false, "show schema-0 to schema-1 backup/write actions")
		apply := flags.Bool("yes", false, "write backups and atomically publish upgraded state")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*dryRun && *apply) {
			return exitUsage
		}
		leaseStatus, err := (lease.Store{}).Inspect()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitIntegrity
		}
		if leaseStatus.Active {
			fmt.Fprintf(stderr, "state upgrade requires no active private lease; stop project %s first\n", leaseStatus.Lease.ProjectID)
			return exitLease
		}
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		projectValue, err := project.Open(cwd)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitIntegrity
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		report, err := state.UpgradeProject(ctx, projectValue, version.Version, *apply)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitIntegrity
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, report)
		}
		if len(report.Actions) == 0 {
			fmt.Fprintln(stdout, "state already uses the current schema")
			return exitOK
		}
		for _, action := range report.Actions {
			verb := "would upgrade"
			if action.Applied {
				verb = "upgraded"
			}
			fmt.Fprintf(stdout, "%s %s schema %d→%d (backup %s)\n", verb, action.Path, action.FromSchema, action.ToSchema, action.Backup)
		}
		return exitOK
	}
	if args[0] != "purge-keys" {
		fmt.Fprintln(stderr, "usage: piglet project purge-keys|upgrade-state [options]")
		return exitUsage
	}
	flags := flag.NewFlagSet("project purge-keys", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dryRun := flags.Bool("dry-run", false, "show exact owned key paths without deleting (the default)")
	apply := flags.Bool("yes", false, "delete exact owned key paths after node absence preflight")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*dryRun && *apply) {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := (privatevm.Manager{PigletVersion: version.Version}).PurgeKeys(ctx, *apply)
	if err != nil {
		fmt.Fprintln(stderr, err)
		var stateErr *privatevm.KeyPurgeStateError
		if errors.As(err, &stateErr) {
			return exitConflict
		}
		return exitIntegrity
	}
	if *jsonOutput {
		return encodeJSON(stdout, stderr, report)
	}
	if len(report.Actions) == 0 {
		fmt.Fprintln(stdout, "no project keys")
		return exitOK
	}
	for _, action := range report.Actions {
		verb := "would delete"
		if action.Applied {
			verb = "deleted"
		}
		fmt.Fprintf(stdout, "%s %s\n", verb, action.Path)
	}
	return exitOK
}

func runPrivateSSH(commandName string, args []string, resolved spec.Resolved, stdout, stderr io.Writer) int {
	node := ""
	command := args
	if len(command) > 0 && command[0] != "--" {
		for _, candidate := range resolved.Nodes {
			if candidate.Name == command[0] {
				node = command[0]
				command = command[1:]
				break
			}
		}
	}
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if commandName == "exec" && len(command) == 0 {
		fmt.Fprintln(stderr, "usage: piglet exec [node] -- command [args...]")
		return exitUsage
	}
	manager := privatevm.Manager{PigletVersion: version.Version}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, err := manager.Connection(ctx, node)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitCapability
	}
	sshArgs := vm.SSHArgsForUser(connection.User, connection.PrivateKey, connection.KnownHosts, connection.Port, command...)
	if sshArgs == nil {
		fmt.Fprintln(stderr, "resolved private SSH user is unsafe")
		return exitIntegrity
	}
	sshCommand := exec.CommandContext(ctx, sshPath, sshArgs...)
	sshCommand.Stdin = os.Stdin
	sshCommand.Stdout = stdout
	sshCommand.Stderr = stderr
	if err := sshCommand.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			if exitError.ExitCode() == 255 {
				return exitRuntime
			}
			return exitError.ExitCode()
		}
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	return exitOK
}

func quickSSHArgs(connection quick.Connection, command []string) ([]string, error) {
	args := vm.SSHArgsForUser(connection.User, connection.PrivateKey, connection.KnownHosts, connection.Port, command...)
	if args == nil {
		return nil, errors.New("resolved quick SSH user is unsafe")
	}
	return args, nil
}

func runSSH(commandName string, args []string, stdout, stderr io.Writer) int {
	if resolved, err := currentProjectResolved(); err == nil && resolved.Network == "private" {
		return runPrivateSSH(commandName, args, resolved, stdout, stderr)
	} else if err != nil {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			if _, markerErr := os.Lstat(filepath.Join(cwd, ".piglet", "project.json")); markerErr == nil {
				fmt.Fprintln(stderr, "refuse SSH dispatch with unreadable project state:", err)
				return exitIntegrity
			}
		}
	}
	command := args
	if len(command) > 0 && command[0] == "meta" {
		command = command[1:]
	}
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if commandName == "exec" && len(command) == 0 {
		fmt.Fprintln(stderr, "usage: piglet exec [meta] -- command [args...]")
		return exitUsage
	}
	operationID, err := project.NewUUID()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	manager := quick.Manager{PigletVersion: version.Version, OperationID: operationID}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, err := manager.Connection(ctx)
	if err != nil {
		_ = manager.RecordEvent(ctx, commandName, "error", err.Error())
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		_ = manager.RecordEvent(ctx, commandName, "error", err.Error())
		fmt.Fprintln(stderr, err)
		return exitCapability
	}
	request := "interactive SSH session requested"
	if len(command) > 0 {
		request = fmt.Sprintf("remote command requested with %d arguments", len(command))
	}
	if err := manager.RecordEvent(ctx, commandName, "info", request); err != nil {
		fmt.Fprintln(stderr, "refuse SSH operation without an auditable event append:", err)
		return exitIntegrity
	}
	sshArgs, err := quickSSHArgs(connection, command)
	if err != nil {
		_ = manager.RecordEvent(ctx, commandName, "error", err.Error())
		fmt.Fprintln(stderr, err)
		return exitIntegrity
	}
	sshCommand := exec.CommandContext(ctx, sshPath, sshArgs...)
	sshCommand.Stdin = os.Stdin
	sshCommand.Stdout = stdout
	sshCommand.Stderr = stderr
	if err := sshCommand.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			_ = manager.RecordEvent(ctx, commandName, "error", fmt.Sprintf("SSH process exited with code %d", exitError.ExitCode()))
			if exitError.ExitCode() == 255 {
				return exitRuntime
			}
			return exitError.ExitCode()
		}
		_ = manager.RecordEvent(ctx, commandName, "error", err.Error())
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	if err := manager.RecordEvent(ctx, commandName, "info", "SSH process completed successfully"); err != nil {
		fmt.Fprintln(stderr, "SSH succeeded but its event could not be appended:", err)
		return exitIntegrity
	}
	return exitOK
}

func encodeJSON(out, errOut io.Writer, value any) int {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(errOut, "encode JSON output: %v\n", err)
		return exitRuntime
	}
	return exitOK
}

func printQuickStatus(out io.Writer, status quick.Status) {
	sshUser := status.SSHUser
	if sshUser == "" {
		sshUser = "dba"
	}
	fmt.Fprintf(out, "%s: %s\n", status.Node, status.State)
	fmt.Fprintf(out, "  ssh: %s@%s:%d\n", sshUser, status.SSHHost, status.SSHPort)
	for _, forward := range status.Forwards {
		if forward.Guest == 22 {
			continue
		}
		fmt.Fprintf(out, "  forward: %s:%d -\u003e :%d\n", forward.Bind, forward.Host, forward.Guest)
	}
	if status.Message != "" {
		fmt.Fprintf(out, "  %s\n", status.Message)
	}
}

func buildQuickOptions(imageAlias string, cpus int, memoryText, rootDiskText, dataDiskText string, noDataDisk, noDefaultForwards bool, forwardTexts repeatedForward, stderr io.Writer) (quick.Options, int) {
	options := quick.Options{Image: imageAlias, CPUs: cpus, NoDataDisk: noDataDisk, NoDefaultForwards: noDefaultForwards}
	for _, sizeFlag := range []struct {
		text   string
		target *int64
	}{{memoryText, &options.Memory}, {rootDiskText, &options.RootDisk}, {dataDiskText, &options.DataDisk}} {
		if sizeFlag.text == "" {
			continue
		}
		value, err := config.ParseSize(sizeFlag.text)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return quick.Options{}, exitUsage
		}
		*sizeFlag.target = value
	}
	for _, text := range forwardTexts {
		forward, err := config.ParseForward(text)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return quick.Options{}, exitUsage
		}
		if forward.Bind != "127.0.0.1" && forward.Bind != "::1" {
			fmt.Fprintf(stderr, "warning: forward %s binds beyond loopback\n", text)
		}
		options.Forwards = append(options.Forwards, forward)
	}
	if _, err := options.Resolve(); err != nil {
		fmt.Fprintln(stderr, err)
		return quick.Options{}, exitUsage
	}
	return options, exitOK
}

func currentProjectResolved() (spec.Resolved, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return spec.Resolved{}, err
	}
	projectValue, err := project.Open(cwd)
	if err != nil {
		return spec.Resolved{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil {
		return spec.Resolved{}, err
	}
	return projectState.Resolved, nil
}

func guardUnsupportedPrivate(command string, stderr io.Writer) (bool, int) {
	resolved, err := currentProjectResolved()
	if err == nil {
		if resolved.Network == "private" {
			fmt.Fprintf(stderr, "piglet %s is not yet implemented for private projects; no quick/meta fallback was executed\n", command)
			return true, exitCapability
		}
		return false, exitOK
	}
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if _, markerErr := os.Lstat(filepath.Join(cwd, ".piglet", "project.json")); markerErr == nil {
			fmt.Fprintf(stderr, "refuse %s with unreadable project state: %v\n", command, err)
			return true, exitIntegrity
		}
	}
	return false, exitOK
}

func printPrivateStatus(out io.Writer, status privatevm.Status) {
	fmt.Fprintf(out, "project: %s\nspec_hash: %s\n", status.ProjectID, status.SpecHash)
	for _, node := range status.Nodes {
		fmt.Fprintf(out, "%s: state=%s runtime=%s private=%s ssh=%s:%d", node.Name, node.State, node.Runtime, node.Address, node.SSHHost, node.SSHPort)
		if node.ProcessID > 0 {
			fmt.Fprintf(out, " pid=%d", node.ProcessID)
		}
		fmt.Fprintln(out)
	}
	if status.Message != "" {
		fmt.Fprintln(out, status.Message)
	}
}

type persistentDeleteError struct{ err error }

func (e *persistentDeleteError) Error() string { return "delete persistent disks: " + e.err.Error() }
func (e *persistentDeleteError) Unwrap() error { return e.err }

func runPrivateCommand(command string, resolved spec.Resolved, nodes []string, force, deletePersistent, noWait, rollback, jsonOutput bool, stdout, stderr io.Writer) int {
	operationID := ""
	if command != "status" && command != "plan" {
		var err error
		operationID, err = project.NewUUID()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
	}
	manager := privatevm.Manager{PigletVersion: version.Version, OperationID: operationID, NoWait: noWait, RollbackFailed: rollback, Nodes: append([]string(nil), nodes...)}
	if command == "plan" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		plan, err := manager.Plan(ctx, resolved)
		if err != nil {
			if errors.Is(err, project.ErrDataRootMigrationRequired) {
				fmt.Fprintln(stderr, err)
				return exitConflict
			}
			var networkPreflight *privatevm.NetworkPreflightError
			if errors.As(err, &networkPreflight) {
				if jsonOutput {
					_ = encodeJSON(stdout, stderr, networkPreflight.Report)
				}
				fmt.Fprintln(stderr, networkPreflight)
				return networkPreflight.Report.ExitCode
			}
			var capability *privatevm.CapabilityError
			if errors.As(err, &capability) {
				fmt.Fprintln(stderr, capability)
				return exitCapability
			}
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if jsonOutput {
			return encodeJSON(stdout, stderr, plan)
		}
		fmt.Fprintf(stdout, "action: %s\ndestructive: %t\nspec_hash: %s\nnodes: %s\n", plan.Action, plan.Destructive, plan.SpecHash, strings.Join(plan.Nodes, ","))
		return exitOK
	}
	timeout := 15 * time.Second
	var operation func(context.Context) (privatevm.Status, error)
	switch command {
	case "up":
		timeout = withReadinessTimeout(15*time.Minute, resolved)
		operation = func(ctx context.Context) (privatevm.Status, error) { return manager.Up(ctx, resolved) }
	case "start":
		timeout, operation = withReadinessTimeout(10*time.Minute, resolved), manager.Start
	case "stop":
		timeout, operation = 5*time.Minute, manager.Stop
	case "restart":
		timeout, operation = withReadinessTimeout(15*time.Minute, resolved), manager.Restart
	case "recreate":
		if err := confirmCLIAction(force, "private recreate", stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		timeout = withReadinessTimeout(20*time.Minute, resolved)
		operation = func(ctx context.Context) (privatevm.Status, error) { return manager.RecreateResolved(ctx, resolved) }
	case "status":
		operation = manager.Status
	case "destroy":
		if err := confirmCLIAction(force, "private destroy", stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		timeout = 10 * time.Minute
		operation = func(ctx context.Context) (privatevm.Status, error) {
			status, err := manager.Destroy(ctx)
			if err != nil || !deletePersistent {
				return status, err
			}
			deleted, err := manager.DeletePersistent(ctx)
			if err != nil {
				return status, &persistentDeleteError{err: err}
			}
			status.Message = fmt.Sprintf("%s; explicitly deleted %d persistent data disk(s)", status.Message, len(deleted))
			return status, nil
		}
	default:
		fmt.Fprintf(stderr, "unsupported private command %q\n", command)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	status, err := operation(ctx)
	if command != "status" {
		level := "info"
		message := status.Message
		if err != nil {
			level = "error"
			message = err.Error()
		}
		eventErr := manager.RecordEvent(ctx, command, level, message)
		if err == nil && eventErr != nil {
			err = fmt.Errorf("private %s completed but its event could not be appended: %w", command, eventErr)
		}
	}
	if err != nil {
		if errors.Is(err, project.ErrDataRootMigrationRequired) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "data_root_migration_required", "operation_id": operationID, "message": err.Error()})
			}
			fmt.Fprintln(stderr, err)
			return exitConflict
		}
		if errors.Is(err, privatevm.ErrRecreateRequired) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "recreate_required", "operation_id": operationID, "message": err.Error()})
			}
			fmt.Fprintln(stderr, err)
			return exitConflict
		}
		var networkPreflight *privatevm.NetworkPreflightError
		if errors.As(err, &networkPreflight) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, networkPreflight.Report)
			}
			fmt.Fprintln(stderr, networkPreflight)
			return networkPreflight.Report.ExitCode
		}
		var capability *privatevm.CapabilityError
		if errors.As(err, &capability) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "capability", "operation_id": operationID, "message": capability.Error()})
			}
			fmt.Fprintln(stderr, capability)
			return exitCapability
		}
		var partial *privatevm.PartialError
		if errors.As(err, &partial) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]any{"error": "partial", "operation_id": operationID, "nodes": partial.Nodes, "rolled_back": partial.RolledBack, "message": partial.Error()})
			}
			fmt.Fprintln(stderr, partial)
			return exitPartial
		}
		var leaseConflict *lease.ConflictError
		if errors.As(err, &leaseConflict) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "lease_conflict", "operation_id": operationID, "message": leaseConflict.Error()})
			}
			fmt.Fprintln(stderr, leaseConflict)
			return exitLease
		}
		var deleteErr *persistentDeleteError
		if errors.As(err, &deleteErr) {
			if jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "persistent_delete", "operation_id": operationID, "message": deleteErr.Error()})
			}
			fmt.Fprintln(stderr, deleteErr)
			return exitIntegrity
		}
		if jsonOutput {
			_ = encodeJSON(stdout, stderr, map[string]string{"error": err.Error(), "operation_id": operationID})
		}
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	status.OperationID = operationID
	if command == "up" || command == "start" || command == "restart" || command == "recreate" {
		seen := make(map[string]struct{})
		for _, node := range resolved.Nodes {
			alias := node.Image
			if alias == "" {
				alias = resolved.Image
			}
			if _, duplicate := seen[alias]; duplicate {
				continue
			}
			seen[alias] = struct{}{}
			if info, infoErr := (quick.Manager{PigletVersion: version.Version}).ImageInfo(ctx, alias); infoErr == nil {
				printImageStatusWarning(stderr, info.Entry)
			}
		}
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, status)
	}
	printPrivateStatus(stdout, status)
	return exitOK
}

func runQuickCommand(command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	force := flags.Bool("force", false, "confirm destructive operation")
	deletePersistent := flags.Bool("delete-persistent", false, "destroy, then explicitly delete owned persistent data disks (requires --force)")
	noWait := flags.Bool("no-wait", false, "return after QMP/process identity instead of guest SSH/ready marker")
	rollback := flags.Bool("rollback", false, "private up: remove only safe artifacts from nodes that fail during this prepare")
	restartDrift := flags.Bool("restart", false, "stop a running VM to apply restart/stop-class drift")
	logLevel := flags.String("log-level", "info", "QEMU diagnostic level: error, warn, info, or debug")
	configPath := flags.String("f", "", "declarative configuration file")
	imageAlias := flags.String("image", "", "quick image alias")
	cpus := flags.Int("cpus", 0, "quick CPU count")
	memoryText := flags.String("memory", "", "quick memory size")
	rootDiskText := flags.String("root-disk", "", "quick root disk size")
	dataDiskText := flags.String("data-disk", "", "quick data disk size")
	noDataDisk := flags.Bool("no-data-disk", false, "disable default /data disk")
	noDefaultForwards := flags.Bool("no-default-forwards", false, "disable four quick business forwards")
	var forwardTexts repeatedForward
	flags.Var(&forwardTexts, "forward", "append [bind:]host:guest TCP forward")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	nodes := flags.Args()
	if len(nodes) != 0 && command != "up" && command != "plan" && command != "start" && command != "stop" && command != "restart" && command != "recreate" && command != "status" {
		fmt.Fprintf(stderr, "%s does not accept node selectors\n", command)
		return exitUsage
	}
	if *deletePersistent && command != "destroy" {
		fmt.Fprintln(stderr, "--delete-persistent is only valid with piglet destroy")
		return exitUsage
	}
	if *deletePersistent && !*force {
		fmt.Fprintln(stderr, "--delete-persistent requires the separate --force destroy confirmation")
		return exitUsage
	}
	if *noWait && command != "up" && command != "start" && command != "restart" && command != "recreate" {
		fmt.Fprintf(stderr, "--no-wait is not valid with piglet %s\n", command)
		return exitUsage
	}
	if *rollback && command != "up" {
		fmt.Fprintln(stderr, "--rollback is only valid with piglet up")
		return exitUsage
	}
	if command != "up" && command != "plan" {
		invalid := ""
		flags.Visit(func(value *flag.Flag) {
			if value.Name != "json" && value.Name != "log-level" && !((command == "destroy" || command == "recreate") && value.Name == "force") && !(command == "destroy" && value.Name == "delete-persistent") && !((command == "start" || command == "restart" || command == "recreate") && value.Name == "no-wait") && !(command == "recreate" && value.Name == "f") {
				invalid = value.Name
			}
		})
		if invalid != "" {
			fmt.Fprintf(stderr, "--%s is only valid with piglet up\n", invalid)
			return exitUsage
		}
	} else if *force {
		fmt.Fprintf(stderr, "--force is not valid with piglet %s\n", command)
		return exitUsage
	}
	if command == "plan" && *restartDrift {
		fmt.Fprintln(stderr, "--restart is only valid with piglet up")
		return exitUsage
	}
	if !quick.ValidLogLevel(*logLevel) {
		fmt.Fprintf(stderr, "invalid --log-level %q\n", *logLevel)
		return exitUsage
	}
	manager := quick.Manager{PigletVersion: version.Version, LogLevel: *logLevel, NoWait: *noWait}
	options, optionCode := buildQuickOptions(*imageAlias, *cpus, *memoryText, *rootDiskText, *dataDiskText, *noDataDisk, *noDefaultForwards, forwardTexts, stderr)
	if optionCode != exitOK {
		return optionCode
	}
	resolvedFile := spec.Resolved{}
	hasConfig := false
	if command == "up" || command == "plan" || command == "recreate" {
		path := *configPath
		if path == "" {
			if cwd, err := os.Getwd(); err == nil {
				candidate := filepath.Join(cwd, "piglet.yaml")
				if _, err := os.Stat(candidate); err == nil {
					path = candidate
				}
			}
		}
		if path != "" {
			absolute, err := filepath.Abs(path)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitUsage
			}
			file, err := config.Load(absolute)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitUsage
			}
			resolvedFile, err = file.Resolve()
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitUsage
			}
			if options.HasOverrides() {
				resolvedFile, err = quick.ApplyOptions(resolvedFile, options)
				if err != nil {
					fmt.Fprintln(stderr, err)
					return exitUsage
				}
			}
			hasConfig = true
		}
	}
	privateRequest := hasConfig && resolvedFile.Network == "private"
	persisted, persistedErr := currentProjectResolved()
	if persistedErr == nil && persisted.Network == "private" {
		if hasConfig && resolvedFile.Network != "private" {
			fmt.Fprintln(stderr, "current project is private; refuse dispatch to the quick/meta runtime")
			return exitConflict
		}
		if !hasConfig {
			resolvedFile = persisted
			hasConfig = true
		}
		privateRequest = true
	} else if persistedErr != nil && !(privateRequest && errors.Is(persistedErr, os.ErrNotExist)) {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			marker := filepath.Join(cwd, ".piglet", "project.json")
			if _, markerErr := os.Lstat(marker); markerErr == nil {
				fmt.Fprintln(stderr, "refuse lifecycle dispatch with an unreadable persisted project:", persistedErr)
				return exitIntegrity
			}
		}
	}
	if privateRequest {
		printWarnings(stderr, configurationWarnings(resolvedFile))
		if *restartDrift {
			fmt.Fprintln(stderr, "--restart drift policy is not yet available for private projects")
			return exitUsage
		}
		return runPrivateCommand(command, resolvedFile, nodes, *force, *deletePersistent, *noWait, *rollback, *jsonOutput, stdout, stderr)
	}
	if *rollback {
		fmt.Fprintln(stderr, "--rollback is only valid for declarative private up")
		return exitUsage
	}
	if len(nodes) != 0 && (len(nodes) != 1 || nodes[0] != "meta") {
		fmt.Fprintf(stderr, "quick project has only node meta, got %v\n", nodes)
		return exitUsage
	}
	if command == "up" || command == "plan" || command == "recreate" {
		warningResolved := resolvedFile
		if !hasConfig {
			if persistedErr == nil && !options.HasOverrides() {
				warningResolved = persisted
			} else if candidate, err := options.Resolve(); err == nil {
				warningResolved = candidate
			}
		}
		printWarnings(stderr, configurationWarnings(warningResolved))
	}
	if command == "plan" {
		var plan quick.Plan
		var err error
		if hasConfig {
			plan, err = manager.PlanResolved(context.Background(), resolvedFile)
		} else {
			plan, err = manager.Plan(context.Background(), options)
		}
		if err != nil {
			if errors.Is(err, project.ErrDataRootMigrationRequired) {
				fmt.Fprintln(stderr, err)
				return exitConflict
			}
			if *jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": err.Error()})
			}
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, plan)
		}
		fmt.Fprintf(stdout, "action: %s\ndestructive: %t\nspec_hash: %s\n", plan.Action, plan.Destructive, plan.SpecHash)
		return exitOK
	}
	operationID := ""
	if command != "status" {
		generatedID, operationErr := project.NewUUID()
		if operationErr != nil {
			fmt.Fprintln(stderr, operationErr)
			return exitRuntime
		}
		operationID = generatedID
		manager.OperationID = operationID
	}
	timeout := 15 * time.Second
	timeoutResolved := resolvedFile
	if !hasConfig {
		if persistedErr == nil {
			timeoutResolved = persisted
		} else if candidate, resolveErr := options.Resolve(); resolveErr == nil {
			timeoutResolved = candidate
		}
	}
	var operation func(context.Context) (quick.Status, error)
	switch command {
	case "up":
		timeout = withReadinessTimeout(10*time.Minute, timeoutResolved)
		if hasConfig {
			operation = func(ctx context.Context) (quick.Status, error) {
				return manager.UpResolvedWithPolicy(ctx, resolvedFile, quick.UpPolicy{Restart: *restartDrift})
			}
		} else {
			operation = func(ctx context.Context) (quick.Status, error) {
				return manager.UpWithOptionsPolicy(ctx, options, quick.UpPolicy{Restart: *restartDrift})
			}
		}
	case "start":
		timeout, operation = withReadinessTimeout(5*time.Minute, timeoutResolved), manager.Start
	case "restart":
		timeout, operation = withReadinessTimeout(7*time.Minute, timeoutResolved), manager.Restart
	case "recreate":
		if err := confirmCLIAction(*force, "recreate", stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		timeout = withReadinessTimeout(12*time.Minute, timeoutResolved)
		if hasConfig {
			operation = func(ctx context.Context) (quick.Status, error) { return manager.RecreateResolved(ctx, resolvedFile) }
		} else {
			operation = manager.Recreate
		}
	case "stop":
		timeout, operation = 2*time.Minute, manager.Stop
	case "status":
		operation = manager.Status
	case "destroy":
		if err := confirmCLIAction(*force, "destroy", stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		timeout = 2 * time.Minute
		operation = func(ctx context.Context) (quick.Status, error) {
			status, err := manager.Destroy(ctx)
			if err != nil || !*deletePersistent {
				return status, err
			}
			deleted, err := manager.DeletePersistent(ctx)
			if err != nil {
				return status, &persistentDeleteError{err: err}
			}
			status.Message = fmt.Sprintf("%s; explicitly deleted %d persistent data disk(s)", status.Message, len(deleted))
			return status, nil
		}
	default:
		fmt.Fprintf(stderr, "unsupported quick command %q\n", command)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if command == "destroy" || command == "recreate" {
		if eventErr := manager.RecordEvent(ctx, "destroy", "info", "scoped destroy requested"); eventErr != nil {
			fmt.Fprintln(stderr, "refuse destroy without an auditable event append:", eventErr)
			return exitIntegrity
		}
	}
	status, err := operation(ctx)
	if operationID != "" {
		status.OperationID = operationID
	}
	if command != "status" && command != "destroy" {
		level := "info"
		message := status.Message
		if err != nil {
			level = "error"
			message = err.Error()
		}
		eventErr := manager.RecordEvent(ctx, command, level, message)
		if err == nil && eventErr != nil {
			err = fmt.Errorf("%s completed but its event could not be appended: %w", command, eventErr)
		}
	}
	if err != nil {
		if errors.Is(err, project.ErrDataRootMigrationRequired) {
			if *jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "data_root_migration_required", "operation_id": operationID, "message": err.Error()})
			}
			fmt.Fprintln(stderr, err)
			return exitConflict
		}
		var drift *quick.DriftError
		if errors.As(err, &drift) {
			if *jsonOutput {
				result := struct {
					Error       string      `json:"error"`
					OperationID string      `json:"operation_id,omitempty"`
					Action      string      `json:"action"`
					Before      interface{} `json:"before"`
					After       interface{} `json:"after"`
				}{Error: "drift", OperationID: operationID, Action: drift.Action, Before: drift.Before, After: drift.After}
				if code := encodeJSON(stdout, stderr, result); code != exitOK {
					return code
				}
			}
			fmt.Fprintln(stderr, err)
			return exitConflict
		}
		var capability *quick.CapabilityError
		if errors.As(err, &capability) {
			if *jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "capability", "operation_id": operationID, "message": capability.Error()})
			}
			fmt.Fprintln(stderr, capability)
			return exitCapability
		}
		var leaseConflict *lease.ConflictError
		if errors.As(err, &leaseConflict) {
			if *jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "lease_conflict", "operation_id": operationID, "message": leaseConflict.Error()})
			}
			fmt.Fprintln(stderr, leaseConflict)
			return exitLease
		}
		var deleteErr *persistentDeleteError
		if errors.As(err, &deleteErr) {
			if *jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": "persistent_delete", "operation_id": operationID, "message": deleteErr.Error()})
			}
			fmt.Fprintln(stderr, deleteErr)
			return exitIntegrity
		}
		if *jsonOutput {
			if code := encodeJSON(stdout, stderr, map[string]string{"error": err.Error()}); code != exitOK {
				return code
			}
		}
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	if command == "up" || command == "start" || command == "restart" || command == "recreate" {
		if status.Image.Alias != "" {
			if info, infoErr := manager.ImageInfo(ctx, status.Image.Alias); infoErr == nil {
				printImageStatusWarning(stderr, info.Entry)
			}
		}
	}
	if *jsonOutput {
		if code := encodeJSON(stdout, stderr, status); code != exitOK {
			return code
		}
	} else if status.Node != "" {
		printQuickStatus(stdout, status)
	}
	return exitOK
}

func runSSHConfig(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ssh-config", flag.ContinueOnError)
	flags.SetOutput(stderr)
	install := flags.Bool("install", false, "install a marker-owned Include and project fragment")
	remove := flags.Bool("remove", false, "remove only this project's marker-owned Include and fragment")
	name := flags.String("name", "piglet", "safe SSH Host/file prefix")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || (*install && *remove) {
		fmt.Fprintln(stderr, "usage: piglet ssh-config [--install|--remove] [--name name] [--json] [node...]")
		return exitUsage
	}
	nodes := flags.Args()
	if *remove && len(nodes) != 0 {
		fmt.Fprintln(stderr, "ssh-config --remove removes the project fragment and does not accept node selectors")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Removal is intentionally state-independent: destroy preserves the
	// matching project marker, and sshconfig.Remove itself deletes only the
	// exact marker-owned fragment and Include. This keeps rollback available
	// after resolved state or node artifacts are gone.
	if *remove {
		result, err := (quick.Manager{PigletVersion: version.Version}).RemoveSSHConfig(*name, "")
		if err != nil {
			return reportSSHConfigFailure(result, err, *jsonOutput, stdout, stderr)
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, result)
		}
		fmt.Fprintf(stdout, "%s SSH config %s (changed=%t)\nfragment: %s\nconfig: %s\n", result.Action, *name, result.Changed, result.Fragment, result.Config)
		return exitOK
	}
	if resolved, err := currentProjectResolved(); err == nil && resolved.Network == "private" {
		manager := privatevm.Manager{PigletVersion: version.Version, Nodes: append([]string(nil), nodes...)}
		if *install {
			var result sshconfig.Result
			result, err = manager.InstallSSHConfig(ctx, *name, "")
			if err != nil {
				return reportSSHConfigFailure(result, err, *jsonOutput, stdout, stderr)
			}
			if *jsonOutput {
				return encodeJSON(stdout, stderr, result)
			}
			fmt.Fprintf(stdout, "%s SSH config %s (changed=%t)\nfragment: %s\nconfig: %s\n", result.Action, *name, result.Changed, result.Fragment, result.Config)
			return exitOK
		}
		text, err := manager.SSHConfig(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, map[string]string{"config": text})
		}
		fmt.Fprint(stdout, text)
		return exitOK
	} else if err != nil {
		if guarded, code := guardUnsupportedPrivate("ssh-config", stderr); guarded {
			return code
		}
	}
	if len(nodes) != 0 && (len(nodes) != 1 || nodes[0] != "meta") {
		fmt.Fprintf(stderr, "quick ssh-config accepts only node meta, got %v\n", nodes)
		return exitUsage
	}
	manager := quick.Manager{PigletVersion: version.Version}
	if *install {
		var result sshconfig.Result
		var err error
		result, err = manager.InstallSSHConfig(ctx, *name, "")
		if err != nil {
			return reportSSHConfigFailure(result, err, *jsonOutput, stdout, stderr)
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, result)
		}
		fmt.Fprintf(stdout, "%s SSH config %s (changed=%t)\nfragment: %s\nconfig: %s\n", result.Action, *name, result.Changed, result.Fragment, result.Config)
		return exitOK
	}
	text, err := manager.SSHConfig(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	fmt.Fprint(stdout, text)
	return exitOK
}

func reportSSHConfigFailure(result sshconfig.Result, err error, jsonOutput bool, stdout, stderr io.Writer) int {
	partial := result.Changed && strings.HasSuffix(result.Action, "-partial")
	if jsonOutput {
		payload := struct {
			Error   string           `json:"error"`
			Partial bool             `json:"partial"`
			Result  sshconfig.Result `json:"result"`
		}{Error: err.Error(), Partial: partial, Result: result}
		if code := encodeJSON(stdout, stderr, payload); code != exitOK {
			return code
		}
	}
	if partial {
		fmt.Fprintf(stderr, "SSH config operation partially changed owned state; retry is safe (action=%s fragment=%s config=%s): %v\n", result.Action, result.Fragment, result.Config, err)
	} else {
		fmt.Fprintln(stderr, err)
	}
	return exitIntegrity
}

func runHosts(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != hostconfig.ActionInstall && args[0] != hostconfig.ActionUninstall) {
		fmt.Fprintln(stderr, "usage: piglet hosts install|uninstall [--json] [--yes]")
		return exitUsage
	}
	action := args[0]
	flags := flag.NewFlagSet("hosts "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	apply := flags.Bool("yes", false, "apply the displayed privileged plan")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	manager := privatevm.Manager{PigletVersion: version.Version}
	projectID := ""
	var entries []hostconfig.Entry
	var err error
	if action == hostconfig.ActionInstall {
		projectID, entries, err = manager.HostEntries(ctx)
	} else {
		projectID, err = manager.ProjectID()
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitCapability
	}
	baseRunner := execx.OSRunner{Timeout: 30 * time.Second, OutputLimit: 1 << 20}
	report, err := (hostconfig.Executor{Root: sudoRunner{base: baseRunner}}).Execute(ctx, projectID, action, entries, *apply)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitIntegrity
	}
	if *jsonOutput {
		return encodeJSON(stdout, stderr, report)
	}
	fmt.Fprintf(stdout, "action: %s\nproject_id: %s\ntarget: %s\nchanged: %t\napplied: %t\nbefore_sha256: %s\nafter_sha256: %s\nhelper: %s\nhelper_sha256: %s\nlock: %s\nlock_exists: %t\nlock_retained: %t\n", report.Plan.Action, report.Plan.ProjectID, report.Plan.Target, report.Plan.Changed, report.Applied, report.Plan.BeforeSHA256, report.Plan.AfterSHA256, report.Plan.HelperPath, report.Plan.HelperSHA256, report.Plan.LockPath, report.Plan.LockExists, report.Plan.LockRetained)
	for _, line := range report.Plan.Lines {
		fmt.Fprintln(stdout, line)
	}
	if report.Plan.Changed && !report.Applied {
		fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact marker-owned plan and caching sudo credentials")
	}
	return exitOK
}

func runLogs(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", "serial", "serial, qemu, or events")
	follow := flags.Bool("follow", false, "continue streaming appended log data")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: piglet logs [node] [--source serial|qemu|events] [--follow]")
		return exitUsage
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := ""
	var err error
	if resolved, resolveErr := currentProjectResolved(); resolveErr == nil && resolved.Network == "private" {
		node := ""
		if flags.NArg() == 1 {
			node = flags.Arg(0)
		}
		path, err = (privatevm.Manager{PigletVersion: version.Version}).LogPath(node, *source)
	} else if resolveErr != nil {
		if guarded, code := guardUnsupportedPrivate("logs", stderr); guarded {
			return code
		}
		if flags.NArg() == 1 && flags.Arg(0) != "meta" {
			fmt.Fprintln(stderr, "quick logs accepts only node meta")
			return exitUsage
		}
		path, err = (quick.Manager{PigletVersion: version.Version}).LogPath(ctx, *source)
	} else {
		if flags.NArg() == 1 && flags.Arg(0) != "meta" {
			fmt.Fprintln(stderr, "quick logs accepts only node meta")
			return exitUsage
		}
		path, err = (quick.Manager{PigletVersion: version.Version}).LogPath(ctx, *source)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	handle, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	defer handle.Close()
	if _, err := io.Copy(stdout, handle); err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	for *follow {
		select {
		case <-ctx.Done():
			return exitOK
		case <-time.After(250 * time.Millisecond):
			if _, err := io.Copy(stdout, handle); err != nil {
				fmt.Fprintln(stderr, err)
				return exitRuntime
			}
		}
	}
	return exitOK
}

func runRepair(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("repair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dryRun := flags.Bool("dry-run", false, "show actions without applying them")
	force := flags.Bool("force", false, "apply the displayed ownership-bounded actions")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	nodes := flags.Args()
	if *dryRun && *force {
		fmt.Fprintln(stderr, "--dry-run conflicts with --force")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if resolved, resolveErr := currentProjectResolved(); resolveErr == nil && resolved.Network == "private" {
		report, err := (privatevm.Manager{PigletVersion: version.Version, Nodes: append([]string(nil), nodes...)}).Repair(ctx, *force)
		if *jsonOutput {
			if code := encodeJSON(stdout, stderr, report); code != exitOK {
				return code
			}
		} else if len(report.Actions) == 0 {
			fmt.Fprintln(stdout, "private project needs no repair")
		} else {
			for _, action := range report.Actions {
				verb := "would"
				if action.Applied {
					verb = "applied"
				}
				fmt.Fprintf(stdout, "%s %s node=%s path=%s: %s\n", verb, action.Kind, action.Node, action.Path, action.Reason)
			}
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitIntegrity
		}
		return exitOK
	} else if resolveErr != nil {
		if guarded, code := guardUnsupportedPrivate("repair", stderr); guarded {
			return code
		}
	}
	if len(nodes) != 0 && (len(nodes) != 1 || nodes[0] != "meta") {
		fmt.Fprintf(stderr, "quick repair accepts only node meta, got %v\n", nodes)
		return exitUsage
	}
	operationID, operationErr := project.NewUUID()
	if operationErr != nil {
		fmt.Fprintln(stderr, operationErr)
		return exitRuntime
	}
	manager := quick.Manager{PigletVersion: version.Version, OperationID: operationID}
	report, err := manager.Repair(ctx, *force)
	if err == nil && *force {
		applied := 0
		for _, action := range report.Actions {
			if action.Applied {
				applied++
			}
		}
		if eventErr := manager.RecordEvent(ctx, "repair", "info", fmt.Sprintf("applied %d ownership-bounded repair actions", applied)); eventErr != nil && !errors.Is(eventErr, os.ErrNotExist) {
			err = fmt.Errorf("repair completed but its event could not be appended: %w", eventErr)
		}
	}
	if *jsonOutput {
		if code := encodeJSON(stdout, stderr, report); code != exitOK {
			return code
		}
	} else if len(report.Actions) == 0 {
		fmt.Fprintln(stdout, "no repair actions")
	} else {
		for _, action := range report.Actions {
			state := "would"
			if action.Applied {
				state = "did"
			}
			fmt.Fprintf(stdout, "%s %s %s: %s\n", state, action.Kind, action.Path, action.Reason)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		var blocked *quick.RepairBlockedError
		if errors.As(err, &blocked) {
			return exitIntegrity
		}
		return exitRuntime
	}
	return exitOK
}

func runDebug(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "bundle" {
		fmt.Fprintln(stderr, "usage: piglet debug bundle [--output path] [--json]")
		return exitUsage
	}
	flags := flag.NewFlagSet("debug bundle", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write the mode-0600 tar.gz bundle to this new path")
	jsonOutput := flags.Bool("json", false, "emit stable JSON after generation")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	plan, err := (diagnostics.Builder{Version: version.Version}).Build(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	outputPath := *output
	if outputPath == "" {
		cwd, getwdErr := os.Getwd()
		if getwdErr != nil {
			fmt.Fprintln(stderr, getwdErr)
			return exitRuntime
		}
		outputPath = filepath.Join(cwd, plan.SuggestedName)
	} else {
		outputPath, err = filepath.Abs(outputPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
	}
	if !*jsonOutput {
		fmt.Fprintln(stdout, "bundle will contain:")
		for _, file := range plan.Files {
			fmt.Fprintf(stdout, "  %s (%d bytes)\n", file.Name, file.Size)
		}
		for _, skipped := range plan.Skipped {
			fmt.Fprintf(stdout, "  skipped: %s\n", skipped)
		}
		fmt.Fprintf(stdout, "creating: %s\n", outputPath)
	}
	result, err := diagnostics.WriteBundle(outputPath, plan)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	if *jsonOutput {
		response := struct {
			Files   []diagnostics.BundleFile `json:"files"`
			Skipped []string                 `json:"skipped,omitempty"`
			Result  diagnostics.BundleResult `json:"result"`
		}{Files: plan.Files, Skipped: plan.Skipped, Result: result}
		return encodeJSON(stdout, stderr, response)
	}
	fmt.Fprintf(stdout, "created %s (%d bytes, sha256:%s)\n", result.Path, result.Size, result.SHA256)
	return exitOK
}

func runList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := (quick.Manager{PigletVersion: version.Version}).List(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	if *jsonOutput {
		if code := encodeJSON(stdout, stderr, report); code != exitOK {
			return code
		}
	} else {
		fmt.Fprintf(stdout, "data root: %s\n", report.DataRoot)
		if len(report.Projects) == 0 {
			fmt.Fprintln(stdout, "no registered projects")
		}
		for _, projectValue := range report.Projects {
			marker := " "
			if projectValue.Current {
				marker = "*"
			}
			name := projectValue.Name
			if name == "" {
				name = "uninitialized"
			}
			fmt.Fprintf(stdout, "%s %s %s\n", marker, projectValue.ProjectID, name)
			fmt.Fprintf(stdout, "  root: %s\n", projectValue.Root)
			for _, node := range projectValue.Nodes {
				fmt.Fprintf(stdout, "  %s: %s (persisted %s)", node.Name, node.Actual, node.Persisted)
				if node.SSHPort != 0 {
					fmt.Fprintf(stdout, " ssh=127.0.0.1:%d", node.SSHPort)
				}
				fmt.Fprintln(stdout)
				if node.Message != "" {
					fmt.Fprintf(stdout, "    %s\n", node.Message)
				}
			}
			if projectValue.Integrity != "" {
				fmt.Fprintf(stdout, "  integrity: %s\n", projectValue.Integrity)
			}
		}
		for _, warning := range report.Warnings {
			fmt.Fprintf(stdout, "warning: %s\n", warning)
		}
	}
	for _, projectValue := range report.Projects {
		if projectValue.Integrity != "" {
			return exitIntegrity
		}
	}
	return exitOK
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	filePath := flags.String("f", "", "configuration file")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	source := "builtin:quick"
	resolved := spec.Quick(true, true)
	path := *filePath
	if path == "" {
		candidate := filepath.Join(cwd, "piglet.yaml")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		}
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		file, err := config.Load(absolute)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		resolved, err = file.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		if resolved.DataRoot != "" {
			resolvedRoot, rootErr := project.ResolveDataRootWithConfig(cwd, resolved.DataRoot, nil)
			if rootErr != nil {
				fmt.Fprintln(stderr, rootErr)
				return exitUsage
			}
			resolved.DataRoot = resolvedRoot
		}
		source = absolute
	}
	hash, err := spec.Hash(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	warnings := configurationWarnings(resolved)
	result := struct {
		Valid    bool          `json:"valid"`
		Source   string        `json:"source"`
		SpecHash string        `json:"spec_hash"`
		Resolved spec.Resolved `json:"resolved"`
		Warnings []string      `json:"warnings,omitempty"`
	}{true, source, hash, resolved, warnings}
	if *jsonOutput {
		return encodeJSON(stdout, stderr, result)
	}
	printWarnings(stderr, warnings)
	fmt.Fprintf(stdout, "valid: %s\nspec_hash: %s\n", source, hash)
	return exitOK
}

func runInit(args []string, stdout, stderr io.Writer) int {
	profileName, flagArgs, err := splitInitArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, "usage: piglet init [profile] [--scale 1..64] [--image alias] [--network-cidr RFC1918/24] [--force-uniform-image] [--json]")
		return exitUsage
	}
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit resolved JSON instead of YAML")
	scale := flags.Int("scale", profile.DefaultScale, "multiply profile CPU and memory by 1..64")
	imageOverride := flags.String("image", "", "override the guest image for every node")
	networkCIDR := flags.String("network-cidr", "", "rebase an embedded private profile to one host-global RFC1918 /24")
	forceUniformImage := flags.Bool("force-uniform-image", false, "allow a uniform override of a mixed-distribution profile")
	if err := flags.Parse(flagArgs); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	if profileName == "" {
		profileName = "quick"
	}
	if profileName == "quick" {
		if *scale != profile.DefaultScale || *imageOverride != "" || *networkCIDR != "" || *forceUniformImage {
			fmt.Fprintln(stderr, "quick init does not accept profile overrides; use a named embedded profile")
			return exitUsage
		}
		resolved, err := (quick.Manager{PigletVersion: version.Version}).Resolved()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, resolved)
		}
		file, err := config.FromResolved(resolved)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		data, err := config.Marshal(file)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		_, _ = stdout.Write(data)
		return exitOK
	}
	var selectedNetwork subnet.Layout
	if *networkCIDR != "" {
		selectedNetwork, err = subnet.Parse(*networkCIDR)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
	}
	file, descriptor, err := profile.LoadWithOverrides(profileName, profile.Overrides{Scale: *scale, Image: *imageOverride, ForceUniformImage: *forceUniformImage, NetworkCIDR: *networkCIDR})
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, profile.ErrNotFound) {
			return exitUsage
		}
		return exitConflict
	}
	if *networkCIDR != "" {
		if warning := selectedNetwork.Warning(); warning != "" {
			fmt.Fprintln(stderr, warning)
		}
	}
	if *jsonOutput {
		resolved, err := file.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		return encodeJSON(stdout, stderr, resolved)
	}
	if *scale == profile.DefaultScale && *imageOverride == "" && *networkCIDR == "" && !*forceUniformImage {
		data, _, err := profile.YAML(profileName)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		_, _ = stdout.Write(data)
		return exitOK
	}
	data, err := config.Marshal(file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "# Piglet profile: %s; generated with explicit overrides.\n", descriptor.Name)
	_, _ = stdout.Write(data)
	return exitOK
}

func splitInitArgs(args []string) (string, []string, error) {
	profileName := ""
	flagArgs := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--scale" || argument == "-scale" || argument == "--image" || argument == "-image" || argument == "--network-cidr" || argument == "-network-cidr":
			if index+1 >= len(args) {
				return "", nil, fmt.Errorf("%s requires a value", argument)
			}
			flagArgs = append(flagArgs, argument, args[index+1])
			index++
		case strings.HasPrefix(argument, "-"):
			flagArgs = append(flagArgs, argument)
		default:
			if profileName != "" {
				return "", nil, fmt.Errorf("init accepts one profile, got %q and %q", profileName, argument)
			}
			profileName = argument
		}
	}
	return profileName, flagArgs, nil
}

func runImage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: piglet image list|info|pull|prune|import|sync|reset-manifest")
		return exitUsage
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("image list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		entries, manifestState, err := (quick.Manager{PigletVersion: version.Version}).Images()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, struct {
				Manifest image.ManifestState `json:"manifest"`
				Images   []image.Entry       `json:"images"`
			}{manifestState, entries})
		}
		fmt.Fprintf(stdout, "manifest: %s version=%d highest=%d\n", manifestState.Active, manifestState.ActiveVersion, manifestState.HighestVersion)
		for _, entry := range entries {
			fmt.Fprintf(stdout, "%s %s %s %s %s\n", entry.Alias, entry.Release, entry.Arch, entry.Status, entry.SHA256)
		}
		return exitOK
	case "info", "pull":
		flags := flag.NewFlagSet("image "+args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 1 {
			fmt.Fprintf(stderr, "usage: piglet image %s [--json] <alias>\n", args[0])
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		manager := quick.Manager{PigletVersion: version.Version}
		var info quick.ImageInfo
		var err error
		if args[0] == "pull" {
			info, err = manager.PullImage(ctx, flags.Arg(0))
		} else {
			info, err = manager.ImageInfo(ctx, flags.Arg(0))
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		printImageStatusWarning(stderr, info.Entry)
		if *jsonOutput {
			return encodeJSON(stdout, stderr, info)
		}
		fmt.Fprintf(stdout, "%s %s %s status=%s sha256:%s cached=%t\n", info.Entry.Alias, info.Entry.Release, info.Entry.Arch, info.Entry.Status, info.Entry.SHA256, info.Cached)
		if info.Path != "" {
			fmt.Fprintf(stdout, "path: %s\n", info.Path)
		}
		return exitOK
	case "prune":
		flags := flag.NewFlagSet("image prune", flag.ContinueOnError)
		flags.SetOutput(stderr)
		dryRun := flags.Bool("dry-run", false, "show unreferenced exact cache pairs without deleting")
		yes := flags.Bool("yes", false, "delete the displayed unreferenced exact cache pairs")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*dryRun && *yes) {
			fmt.Fprintln(stderr, "usage: piglet image prune [--dry-run|--yes] [--json]")
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		report, err := (quick.Manager{PigletVersion: version.Version}).PruneImages(ctx, *yes)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitIntegrity
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, report)
		}
		if len(report.Items) == 0 {
			fmt.Fprintln(stdout, "no unreferenced cache images")
			return exitOK
		}
		for _, item := range report.Items {
			action := "would delete"
			if item.Applied {
				action = "deleted"
			}
			fmt.Fprintf(stdout, "%s sha256:%s (%d bytes)\n", action, item.Digest, item.Bytes)
		}
		return exitOK
	case "sync":
		flags := flag.NewFlagSet("image sync", flag.ContinueOnError)
		flags.SetOutput(stderr)
		allowDowngrade := flags.Bool("allow-downgrade", false, "explicitly activate a version below the high-water mark")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: piglet image sync [--allow-downgrade] <url|path>")
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		state, err := (quick.Manager{PigletVersion: version.Version}).ManifestSync(ctx, flags.Arg(0), *allowDowngrade)
		if err != nil {
			if *jsonOutput {
				_ = encodeJSON(stdout, stderr, map[string]string{"error": err.Error()})
			}
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, state)
		}
		fmt.Fprintf(stdout, "activated manifest version %d digest %s key %s\n", state.ActiveVersion, state.ActiveDigest, state.KeyID)
		return exitOK
	case "reset-manifest":
		flags := flag.NewFlagSet("image reset-manifest", flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		state, err := (quick.Manager{PigletVersion: version.Version}).ManifestReset(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		if *jsonOutput {
			return encodeJSON(stdout, stderr, state)
		}
		fmt.Fprintf(stdout, "active manifest reset to embedded version %d; high-water mark %d preserved\n", state.ActiveVersion, state.HighestVersion)
		return exitOK
	case "import":
		flags := flag.NewFlagSet("image import", flag.ContinueOnError)
		flags.SetOutput(stderr)
		expected := flags.String("sha256", "", "optional expected SHA-256")
		name := flags.String("name", "", "optional immutable local alias")
		boot := flags.String("boot", "", "required with --name: bios or uefi")
		sourceUser := flags.String("source-user", "", "required with --name: source image login user")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: piglet image import [--sha256 digest] [--name alias --boot bios|uefi --source-user user] <path>")
			return exitUsage
		}
		invalidAliasMetadata := (*name == "" && (*boot != "" || *sourceUser != "")) || (*name != "" && (*sourceUser == "" || (*boot != "bios" && *boot != "uefi")))
		if invalidAliasMetadata {
			fmt.Fprintln(stderr, "--name requires --boot bios|uefi and --source-user; alias metadata is immutable")
			return exitUsage
		}
		manager := quick.Manager{PigletVersion: version.Version}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var path string
		var metadata image.Metadata
		var localEntry *image.Entry
		var err error
		if *name == "" {
			path, metadata, err = manager.ImportImage(ctx, flags.Arg(0), *expected)
		} else {
			entry, importedPath, importedMetadata, importErr := manager.ImportNamedImage(ctx, flags.Arg(0), *expected, *name, *boot, *sourceUser)
			path, metadata, err = importedPath, importedMetadata, importErr
			if importErr == nil {
				localEntry = &entry
			}
		}
		result := struct {
			Path     string         `json:"path"`
			Metadata image.Metadata `json:"metadata"`
			Entry    *image.Entry   `json:"entry,omitempty"`
		}{Path: path, Metadata: metadata, Entry: localEntry}
		if *jsonOutput {
			if code := encodeJSON(stdout, stderr, result); code != exitOK {
				return code
			}
		} else if path != "" {
			fmt.Fprintf(stdout, "imported %s\nsha256 %s\n", path, metadata.Digest)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown image command %q\n", args[0])
		return exitUsage
	}
}

func runPigsty(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "inventory" {
		fmt.Fprintln(stderr, "usage: piglet pigsty inventory --profile NAME --root ABSOLUTE_PATH [--scale 1..64] [--network-cidr RFC1918/24] [--output ABSOLUTE_PATH --force]")
		return exitUsage
	}
	flags := flag.NewFlagSet("pigsty inventory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "Piglet-owned profile name")
	sourceRoot := flags.String("root", "", "absolute physical Pigsty source root")
	scale := flags.Int("scale", profile.DefaultScale, "profile CPU/memory scale used for inventory tuning")
	networkCIDR := flags.String("network-cidr", subnet.DefaultCIDR, "target host-global RFC1918 /24")
	outputPath := flags.String("output", "", "optional absolute output path")
	force := flags.Bool("force", false, "replace a changed Piglet-managed output file")
	if err := flags.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 || *profileName == "" || *sourceRoot == "" {
		fmt.Fprintln(stderr, "usage: piglet pigsty inventory --profile NAME --root ABSOLUTE_PATH [--scale 1..64] [--network-cidr RFC1918/24] [--output ABSOLUTE_PATH --force]")
		return exitUsage
	}
	if _, err := subnet.Parse(*networkCIDR); err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	if *scale < profile.MinScale || *scale > profile.MaxScale {
		fmt.Fprintln(stderr, "--scale must be in range 1..64")
		return exitUsage
	}
	if !filepath.IsAbs(*sourceRoot) || filepath.Clean(*sourceRoot) != *sourceRoot {
		fmt.Fprintln(stderr, "--root must be a clean absolute path")
		return exitUsage
	}
	if *outputPath != "" && (!filepath.IsAbs(*outputPath) || filepath.Clean(*outputPath) != *outputPath) {
		fmt.Fprintln(stderr, "--output must be a clean absolute path")
		return exitUsage
	}
	if *outputPath == "" && *force {
		fmt.Fprintln(stderr, "--force requires --output")
		return exitUsage
	}

	result, err := pigstyint.RenderScaled(*sourceRoot, *profileName, *networkCIDR, *scale)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, profile.ErrNotFound) {
			return exitUsage
		}
		return exitIntegrity
	}
	if layout, parseErr := subnet.Parse(result.TargetCIDR); parseErr == nil {
		if warning := layout.Warning(); warning != "" {
			fmt.Fprintln(stderr, warning)
		}
	}
	if *outputPath == "" {
		if _, err := stdout.Write(result.Data); err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		return exitOK
	}
	published, err := pigstyint.Publish(*outputPath, result, *force)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, pigstyint.ErrOutputConflict) {
			return exitConflict
		}
		return exitIntegrity
	}
	verb := "already current"
	if published.Changed {
		verb = "wrote"
	}
	fmt.Fprintf(stdout, "%s Pigsty inventory %s with marker %s from %s (%s, %s, scale=%d, %d semantic address tokens, %d replacements, %d overlay changes, %d tune changes)\n", verb, published.Path, published.MarkerPath, result.SourcePath, result.TargetCIDR, result.InventoryMode, result.Scale, result.Matches, result.Replacements, result.OverlayChanges, result.TuneChanges)
	return exitOK
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor does not accept positional arguments")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report := (doctor.Probe{}).Run(ctx)
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode doctor report: %v\n", err)
			return exitRuntime
		}
	} else {
		fmt.Fprintf(stdout, "host: %s/%s (%s)\n", report.OS, report.Arch, report.Tier)
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Evidence)
			if check.Fix != "" {
				fmt.Fprintf(stdout, "  fix: %s\n", check.Fix)
			}
		}
	}
	if report.HasErrors() {
		return exitCapability
	}
	return exitOK
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "up", "start", "stop", "restart", "recreate", "status", "destroy", "plan":
		return runQuickCommand(args[0], args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "ssh", "exec":
		return runSSH(args[0], args[1:], stdout, stderr)
	case "ssh-config":
		return runSSHConfig(args[1:], stdout, stderr)
	case "hosts":
		return runHosts(args[1:], stdout, stderr)
	case "logs":
		return runLogs(args[1:], stdout, stderr)
	case "repair":
		return runRepair(args[1:], stdout, stderr)
	case "image":
		return runImage(args[1:], stdout, stderr)
	case "project":
		return runProject(args[1:], stdout, stderr)
	case "network":
		return runNetwork(args[1:], stdout, stderr)
	case "pigsty":
		return runPigsty(args[1:], stdout, stderr)
	case "debug":
		return runDebug(args[1:], stdout, stderr)
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
	case "version":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "version does not accept arguments")
			return exitUsage
		}
		fmt.Fprintf(stdout, "piglet %s (commit %s, built %s, %s/%s)\n", version.Version, version.Commit, version.Date, runtime.GOOS, runtime.GOARCH)
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

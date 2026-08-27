package main

import (
	"bufio"
	"bytes"
	"context"
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
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/config"
	"github.com/pgsty/farrow/internal/doctor"
	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/hostconfig"
	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/lease"
	darwinnet "github.com/pgsty/farrow/internal/network/darwin"
	linuxnet "github.com/pgsty/farrow/internal/network/linux"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/network/subnet"
	privatevm "github.com/pgsty/farrow/internal/private"
	"github.com/pgsty/farrow/internal/provision"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/pgsty/farrow/internal/sshkeys"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/version"
	"github.com/pgsty/farrow/internal/vm"
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

type lifecycleOptions struct {
	Force            bool
	DeletePersistent bool
	Purge            bool
	NoWait           bool
	Rollback         bool
	ConfigPath       string
	Repository       string
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
		warningf(out, "%s", warning)
	}
}

func printImageStatusWarning(out io.Writer, entry image.Entry) {
	if entry.Status != "" && entry.Status != "supported" {
		warningf(out, "image %s/%s (%s) has status %s, not supported; use only with the corresponding test/risk acceptance", entry.Alias, entry.Arch, entry.Release, entry.Status)
	}
}

func confirmDestructive(force, interactive bool, action string, input io.Reader, output io.Writer) error {
	if force {
		return nil
	}
	if !interactive {
		return fmt.Errorf("%s requires --force when stdin is not a TTY", action)
	}
	fmt.Fprintf(output, "Confirm scoped Farrow %s by typing %q: ", action, action)
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
	file, err := config.LoadPath(absolute)
	if err != nil {
		return spec.Resolved{}, err
	}
	resolved, err := file.Resolve()
	if err != nil {
		return spec.Resolved{}, err
	}
	if resolved.Network != "private" || resolved.Private == nil {
		return spec.Resolved{}, errors.New("network preflight -f requires a valid configuration")
	}
	return resolved, nil
}

type commandFlagSet struct {
	*flag.FlagSet
	stderr io.Writer
}

func newCommandFlagSet(name string, stderr io.Writer) *commandFlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {
		fmt.Fprintf(stderr, "usage: farrow %s [options]\n\noptions:\n", name)
		fmt.Fprintln(stderr, "  --json                   emit JSON output")
		fmt.Fprintln(stderr, "  --yaml                   emit YAML output")
		fmt.Fprintln(stderr, "  --verbose                emit detailed diagnostics to stderr")
		set.VisitAll(func(option *flag.Flag) {
			if option.Name == "json" {
				return
			}
			value, description := flag.UnquoteUsage(option)
			name := "--" + option.Name
			if len(option.Name) == 1 {
				name = "-" + option.Name
			}
			if value != "" {
				name += " " + value
			}
			fmt.Fprintf(stderr, "  %-24s %s\n", name, description)
		})
	}
	return &commandFlagSet{FlagSet: set, stderr: stderr}
}

func (set *commandFlagSet) Parse(arguments []string) error {
	return set.FlagSet.Parse(arguments)
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
		fmt.Fprintln(stderr, "usage: farrow network preflight|status|install|uninstall [--json|--yaml] [--verbose] [--yes]")
		return exitUsage
	}
	command := args[0]
	if command == "preflight" {
		flags := newCommandFlagSet("network preflight", stderr)
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
			errorf(stderr, "%v", err)
			return exitUsage
		}
		addresses := layout.StaticAddresses()
		if *configPath != "" {
			resolved, loadErr := loadPrivatePreflightConfig(*configPath)
			if loadErr != nil {
				errorf(stderr, "%v", loadErr)
				return exitUsage
			}
			configLayout, layoutErr := subnet.Parse(resolved.Private.CIDR)
			if layoutErr != nil {
				errorf(stderr, "%v", layoutErr)
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
		debugf(stderr, "network preflight cidr=%s addresses=%d", layout.CIDR(), len(addresses))
		progressItem := startProgress(ctx, stderr, "Checking host network readiness")
		report := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Inspect, Layout: layout, Addresses: addresses}, netpreflight.Probe{Runner: execx.OSRunner{Timeout: 5 * time.Second, OutputLimit: 1 << 20}})
		progressItem.Stop(nil)
		if structuredOutput(stdout, *jsonOutput) {
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
		flags := newCommandFlagSet("network "+command, stderr)
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
			fmt.Fprintln(stderr, "--mode is a Darwin socket_vmnet option; Linux uses farrow0")
			return exitUsage
		}
		layout, layoutErr := subnet.Parse(*networkCIDR)
		if layoutErr != nil {
			errorf(stderr, "%v", layoutErr)
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
			preflightProgress := startProgress(ctx, stderr, "Validating the private network plan")
			preflightReport := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Install, Layout: layout, Addresses: addresses}, netpreflight.Probe{Runner: baseRunner})
			preflightProgress.Stop(nil)
			if !preflightReport.Ready {
				if structuredOutput(stdout, *jsonOutput) {
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
				progressItem := startProgress(ctx, stderr, "Installing the Darwin private network")
				report, err := installDarwinNetwork(ctx, executor, *archive, *interfaceID, runtime.GOARCH, *vmnetMode, layout.CIDR(), *apply)
				progressItem.Stop(err)
				if err != nil {
					errorf(stderr, "%v", err)
					if errors.Is(err, darwinnet.ErrVMNetSharingBusy) {
						return exitLease
					}
					return exitIntegrity
				}
				if structuredOutput(stdout, *jsonOutput) {
					return encodeJSON(stdout, stderr, report)
				}
				for _, warning := range report.Warnings {
					warningf(stderr, "%s", warning)
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
			progressItem := startProgress(ctx, stderr, "Removing the Darwin private network")
			report, err := executor.Uninstall(ctx, *apply)
			progressItem.Stop(err)
			if err != nil {
				errorf(stderr, "%v", err)
				return exitIntegrity
			}
			if structuredOutput(stdout, *jsonOutput) {
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
				errorf(stderr, "%v", configErr)
				return exitUsage
			}
			progressItem := startProgress(ctx, stderr, "Installing the Linux private network")
			report, err := executor.InstallConfig(ctx, linuxConfig, *apply)
			progressItem.Stop(err)
			if err != nil {
				errorf(stderr, "%v", err)
				return exitIntegrity
			}
			if structuredOutput(stdout, *jsonOutput) {
				return encodeJSON(stdout, stderr, report)
			}
			for _, warning := range report.Warnings {
				warningf(stderr, "%s", warning)
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
		progressItem := startProgress(ctx, stderr, "Removing the Linux private network")
		report, err := executor.Uninstall(ctx, *apply)
		progressItem.Stop(err)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitIntegrity
		}
		if structuredOutput(stdout, *jsonOutput) {
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
		fmt.Fprintln(stderr, "usage: farrow network preflight|status|install|uninstall [--json|--yaml] [--verbose] [--yes]")
		return exitUsage
	}
	flags := newCommandFlagSet("network status", stderr)
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
		errorf(stderr, "%v", err)
		return exitUsage
	}
	baseRunner := execx.OSRunner{Timeout: 5 * time.Second, OutputLimit: 1 << 20}
	progressItem := startProgress(ctx, stderr, "Inspecting the host network")
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
		if check.Name == "kvm" || check.Name == "linux-family" || check.Name == "linux-network-owner" || check.Name == "bridge-helper" {
			checks = append(checks, check)
			if check.Status == doctor.Error {
				hasError = true
			}
		}
	}
	leaseStatus, err := (lease.Store{}).Inspect()
	progressItem.Stop(err)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitIntegrity
	}
	result := struct {
		OS        string              `json:"os"`
		Arch      string              `json:"arch"`
		Preflight netpreflight.Report `json:"preflight"`
		Checks    []doctor.Check      `json:"checks,omitempty"`
		Lease     lease.Status        `json:"lease"`
	}{OS: doctorReport.OS, Arch: doctorReport.Arch, Preflight: preflightReport, Checks: checks, Lease: leaseStatus}
	if structuredOutput(stdout, *jsonOutput) {
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

const structuredCommandCaptureLimit = 4 << 20

type boundedCapture struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (capture *boundedCapture) Write(data []byte) (int, error) {
	written := len(data)
	remaining := capture.limit - capture.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = capture.buffer.Write(data[:remaining])
	}
	if remaining < len(data) {
		capture.truncated = true
	}
	return written, nil
}

func (capture *boundedCapture) String() string { return capture.buffer.String() }

type remoteCommandResult struct {
	Command         string   `json:"command"`
	Interactive     bool     `json:"interactive"`
	SessionStream   string   `json:"session_stream,omitempty"`
	Node            string   `json:"node"`
	User            string   `json:"user"`
	Host            string   `json:"host"`
	Port            uint16   `json:"port"`
	Arguments       []string `json:"arguments"`
	Success         bool     `json:"success"`
	ExitCode        int      `json:"exit_code"`
	DurationMS      int64    `json:"duration_ms"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	StdoutTruncated bool     `json:"stdout_truncated,omitempty"`
	StderrTruncated bool     `json:"stderr_truncated,omitempty"`
	Error           string   `json:"error,omitempty"`
	AuditError      string   `json:"audit_error,omitempty"`
}

func executeSSHProcess(ctx context.Context, commandName, node, user, host string, port uint16, sshPath string, sshArgs, arguments []string, stdout, stderr io.Writer) (remoteCommandResult, error) {
	result := remoteCommandResult{
		Command: commandName, Node: node, User: user, Host: host, Port: port,
		Arguments: append([]string(nil), arguments...), ExitCode: -1,
	}
	sshCommand := exec.CommandContext(ctx, sshPath, sshArgs...)
	sshCommand.Stdin = os.Stdin
	structured := structuredOutput(stdout, false)
	interactiveStructured := structured && len(arguments) == 0
	var capturedStdout, capturedStderr boundedCapture
	if interactiveStructured {
		result.Interactive = true
		result.SessionStream = "stderr"
		sshCommand.Stdout = rawWriter(stderr)
		sshCommand.Stderr = rawWriter(stderr)
	} else if structured {
		capturedStdout.limit = structuredCommandCaptureLimit
		capturedStderr.limit = structuredCommandCaptureLimit
		sshCommand.Stdout = &capturedStdout
		sshCommand.Stderr = &capturedStderr
	} else {
		sshCommand.Stdout = rawWriter(stdout)
		sshCommand.Stderr = rawWriter(stderr)
	}
	started := time.Now()
	err := sshCommand.Run()
	result.DurationMS = time.Since(started).Milliseconds()
	result.Stdout = capturedStdout.String()
	result.Stderr = capturedStderr.String()
	result.StdoutTruncated = capturedStdout.truncated
	result.StderrTruncated = capturedStderr.truncated
	if err == nil {
		result.Success = true
		result.ExitCode = 0
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
	} else {
		result.Error = err.Error()
	}
	return result, err
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
		fmt.Fprintln(stderr, "usage: farrow exec [node] -- command [args...]")
		return exitUsage
	}
	manager := privatevm.Manager{FarrowVersion: version.Version}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, err := manager.Connection(ctx, node)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		errorf(stderr, "%v", err)
		return exitCapability
	}
	sshArgs := vm.SSHArgsForUser(connection.User, connection.PrivateKey, connection.KnownHosts, connection.Port, command...)
	if sshArgs == nil {
		fmt.Fprintln(stderr, "resolved private SSH user is unsafe")
		return exitIntegrity
	}
	debugf(stderr, "ssh mode=private node=%s user=%s host=%s port=%d arguments=%d", connection.Node, connection.User, connection.Host, connection.Port, len(command))
	result, runErr := executeSSHProcess(ctx, commandName, connection.Node, connection.User, connection.Host, connection.Port, sshPath, sshArgs, command, stdout, stderr)
	if structuredOutput(stdout, false) {
		if code := encodeJSON(stdout, stderr, result); code != exitOK {
			return code
		}
	}
	if runErr != nil {
		var exitError *exec.ExitError
		if errors.As(runErr, &exitError) {
			if exitError.ExitCode() == 255 {
				return exitRuntime
			}
			return exitError.ExitCode()
		}
		if !structuredOutput(stdout, false) {
			errorf(stderr, "%v", runErr)
		}
		return exitRuntime
	}
	return exitOK
}

func runSSH(commandName string, args []string, stdout, stderr io.Writer) int {
	resolved, err := currentProjectResolved()
	if err != nil {
		errorf(stderr, "no deployment state found; run `farrow up` first")
		return exitConflict
	}
	if resolved.Network != "private" {
		errorf(stderr, "this project predates the fixed-IP redesign; recreate it with `farrow destroy --force && farrow up`")
		return exitConflict
	}
	return runPrivateSSH(commandName, args, resolved, stdout, stderr)
}

func printProvisionReport(stdout, stderr io.Writer, report provision.Report) {
	textField(stdout, 10, "script", report.Script.Name)
	textField(stdout, 10, "sha256", report.Script.SHA256)
	textField(stdout, 10, "bytes", report.Script.Size)
	textField(stdout, 10, "sudo", report.Sudo)
	textField(stdout, 10, "parallel", report.Parallelism)
	for _, result := range report.Results {
		state := "failed"
		if result.Success {
			state = "success"
		}
		fmt.Fprintf(stdout, "%-16s %s  exit=%d  duration=%dms\n", result.Node, statusValue(stdout, state), result.ExitCode, result.DurationMS)
		if result.Stdout != "" {
			fmt.Fprintf(stdout, "--- %s stdout ---\n%s", result.Node, result.Stdout)
			if !strings.HasSuffix(result.Stdout, "\n") {
				fmt.Fprintln(stdout)
			}
		}
		if result.Stderr != "" {
			fmt.Fprintf(stderr, "--- %s stderr ---\n%s", result.Node, result.Stderr)
			if !strings.HasSuffix(result.Stderr, "\n") {
				fmt.Fprintln(stderr)
			}
		}
		if result.StdoutTruncated {
			fmt.Fprintf(stderr, "[%s] stdout was truncated at the bounded capture limit\n", result.Node)
		}
		if result.StderrTruncated {
			fmt.Fprintf(stderr, "[%s] stderr was truncated at the bounded capture limit\n", result.Node)
		}
		if result.Error != "" {
			fmt.Fprintf(stderr, "[%s] %s\n", result.Node, result.Error)
		}
	}
	fmt.Fprintf(stdout, "%s %d successful, %d failed, %dms total\n", styled(stdout, ansiBold, "provisioned"), report.Successful, report.Failed, report.DurationMS)
}

func provisionConnectionExit(err error) int {
	var artifactError *sshkeys.SSHArtifactError
	if errors.As(err, &artifactError) {
		return exitIntegrity
	}
	return exitRuntime
}

// purgeDeployment removes what destroy deliberately preserves: keys and the
// deployment state document. Images stay cached; persistent disks were already
// deleted by the forced --purge destroy.
func purgeDeployment(ctx context.Context, stdout io.Writer) error {
	manager := privatevm.Manager{FarrowVersion: version.Version}
	if _, err := manager.PurgeKeys(ctx, true); err != nil {
		return err
	}
	root, err := state.ResolveDataRoot()
	if err != nil {
		return err
	}
	for _, name := range []string{"state.json", "events.jsonl"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, directory := range []string{"nodes", "disks"} {
		if err := os.Remove(filepath.Join(root, directory)); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
			return err
		}
	}
	fmt.Fprintln(stdout, "purged deployment keys and state; images remain cached")
	return nil
}

func runProvision(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("provision", stderr)
	scriptPath := flags.String("script", "", "local Bash script to stream to each selected guest")
	sudo := flags.Bool("sudo", false, "run the fixed guest /bin/bash command through sudo -n")
	parallelism := flags.Int("parallel", 1, "bounded node concurrency, 1..4")
	timeout := flags.Duration("timeout", time.Hour, "hard deadline for the complete operation, maximum 24h")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	nodes := flags.Args()
	if *scriptPath == "" {
		fmt.Fprintln(stderr, "usage: farrow provision --script path [--sudo] [--parallel 1..4] [--timeout 1h] [--json|--yaml] [--verbose] [node...]")
		return exitUsage
	}
	if *parallelism < 1 || *parallelism > provision.MaxParallelism {
		fmt.Fprintf(stderr, "provision parallelism must be 1..%d\n", provision.MaxParallelism)
		return exitUsage
	}
	if *timeout <= 0 || *timeout > 24*time.Hour {
		fmt.Fprintln(stderr, "provision timeout must be greater than zero and no more than 24h")
		return exitUsage
	}
	script, err := provision.LoadScript(*scriptPath)
	if err != nil {
		errorf(stderr, "%v", err)
		if errors.Is(err, os.ErrNotExist) {
			return exitUsage
		}
		return exitIntegrity
	}
	operationID, err := identity.NewUUID()
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	deployment, err := privatevm.Open()
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	deploymentLock, err := privatevm.AcquireLock(ctx, deployment, false)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitConflict
	}
	locked := true
	defer func() {
		if locked {
			_ = deploymentLock.Release()
		}
	}()
	deploymentState, err := (state.Store{Root: deployment.Root}).ReadDeployment()
	if err != nil {
		fmt.Fprintln(stderr, "provision requires an existing running deployment:", err)
		return exitConflict
	}
	resolved := deploymentState.Resolved

	targets := make([]provision.Target, 0)
	selectedNames := make([]string, 0)
	var recordEvent func(context.Context, string, string, string) error
	if resolved.Network == "private" {
		manager := privatevm.Manager{FarrowVersion: version.Version, OperationID: operationID, Nodes: append([]string(nil), nodes...)}
		connections, connectionErr := manager.ConnectionsLocked(ctx, deployment, deploymentLock)
		if connectionErr != nil {
			errorf(stderr, "%v", connectionErr)
			return provisionConnectionExit(connectionErr)
		}
		for _, connection := range connections {
			if connection.Host != "127.0.0.1" {
				fmt.Fprintf(stderr, "refuse non-loopback provision endpoint for node %s\n", connection.Node)
				return exitIntegrity
			}
			targets = append(targets, provision.Target{Node: connection.Node, User: connection.User, Port: connection.Port, PrivateKey: connection.PrivateKey, KnownHosts: connection.KnownHosts})
			selectedNames = append(selectedNames, connection.Node)
		}
		recordEvent = manager.RecordEvent
	} else {
		fmt.Fprintf(stderr, "unsupported project network %q\n", resolved.Network)
		return exitIntegrity
	}

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		errorf(stderr, "%v", err)
		return exitCapability
	}
	sshPath, err = filepath.Abs(sshPath)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitCapability
	}
	startMessage := fmt.Sprintf("script_sha256=%s bytes=%d sudo=%t parallel=%d targets=%s", script.SHA256, script.Size, *sudo, *parallelism, strings.Join(selectedNames, ","))
	if err := recordEvent(ctx, "provision", "info", "starting "+startMessage); err != nil {
		fmt.Fprintln(stderr, "refuse provision without an auditable event append:", err)
		return exitIntegrity
	}
	debugf(stderr, "provision operation_id=%s targets=%s timeout=%s parallel=%d sudo=%t script_sha256=%s", operationID, strings.Join(selectedNames, ","), *timeout, *parallelism, *sudo, script.SHA256)
	progressItem := startProgress(ctx, stderr, fmt.Sprintf("Provisioning %d node(s)", len(targets)))
	report, err := (provision.Executor{
		Runner: provision.SSHRunner{SSHPath: sshPath}, Parallelism: *parallelism, OperationID: operationID,
	}).Execute(ctx, script, targets, *sudo)
	progressItem.Stop(err)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	resultParts := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		resultParts = append(resultParts, fmt.Sprintf("%s=exit:%d,duration:%dms", result.Node, result.ExitCode, result.DurationMS))
	}
	level := "info"
	if report.Failed != 0 {
		level = "error"
	}
	auditContext, auditCancel := context.WithTimeout(context.Background(), 10*time.Second)
	auditErr := recordEvent(auditContext, "provision", level, fmt.Sprintf("completed script_sha256=%s successful=%d failed=%d results=%s", script.SHA256, report.Successful, report.Failed, strings.Join(resultParts, ";")))
	auditCancel()
	if auditErr != nil {
		report.AuditError = "remote execution completed but its audit event could not be appended: " + auditErr.Error()
	}
	releaseErr := deploymentLock.Release()
	locked = false
	if releaseErr != nil {
		if report.AuditError != "" {
			report.AuditError += "; "
		}
		report.AuditError += "release project lock: " + releaseErr.Error()
	}
	if structuredOutput(stdout, *jsonOutput) {
		if code := encodeJSON(stdout, stderr, report); code != exitOK {
			return code
		}
	} else {
		printProvisionReport(stdout, stderr, report)
	}
	if report.AuditError != "" {
		errorf(stderr, "%s", report.AuditError)
		return exitIntegrity
	}
	if report.Failed == 0 {
		return exitOK
	}
	if report.Successful > 0 {
		return exitPartial
	}
	if len(report.Results) == 1 {
		code := report.Results[0].ExitCode
		if code > 0 && code != 255 {
			return code
		}
	}
	return exitRuntime
}

func encodeJSON(out, errOut io.Writer, value any) int {
	if err := encodeOutput(out, value); err != nil {
		fmt.Fprintf(errOut, "encode %s output: %v\n", outputFormatFor(out), err)
		return exitRuntime
	}
	return exitOK
}

func loadLifecycleConfig(command, configPath string) (spec.Resolved, bool, error) {
	if command != "up" && command != "plan" && command != "recreate" && command != "reload" {
		return spec.Resolved{}, false, nil
	}
	file, _, err := config.Discover("", configPath)
	if errors.Is(err, config.ErrNoConfig) {
		return spec.Resolved{}, false, nil
	}
	if err != nil {
		return spec.Resolved{}, false, err
	}
	resolved, err := file.Resolve()
	if err != nil {
		return spec.Resolved{}, false, err
	}
	return resolved, true, nil
}

func currentProjectResolved() (spec.Resolved, error) {
	root, err := state.ResolveDataRoot()
	if err != nil {
		return spec.Resolved{}, err
	}
	deploymentState, err := (state.Store{Root: root}).ReadDeployment()
	if err != nil {
		return spec.Resolved{}, err
	}
	return deploymentState.Resolved, nil
}

func printPrivateStatus(out io.Writer, status privatevm.Status) {
	textField(out, 12, "spec hash", status.SpecHash)
	for _, node := range status.Nodes {
		fmt.Fprintf(out, "%-16s %s  runtime=%s  private=%s  ssh=%s:%d", node.Name, statusValue(out, string(node.State)), node.Runtime, node.Address, node.SSHHost, node.SSHPort)
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

type lifecyclePartialFailure struct {
	Error       string   `json:"error"`
	Message     string   `json:"message"`
	OperationID string   `json:"operation_id,omitempty"`
	Nodes       []string `json:"nodes"`
	RolledBack  []string `json:"rolled_back"`
}

func reportPrivateLifecycleError(err error, operationID string, jsonOutput bool, stdout, stderr io.Writer) int {
	if errors.Is(err, privatevm.ErrRecreateRequired) {
		return reportCommandFailure(stdout, stderr, jsonOutput, "recreate_required", err.Error(), operationID, exitConflict)
	}
	if errors.Is(err, privatevm.ErrNodesRemoved) {
		return reportCommandFailure(stdout, stderr, jsonOutput, "nodes_removed", err.Error(), operationID, exitConflict)
	}
	var networkPreflight *privatevm.NetworkPreflightError
	if errors.As(err, &networkPreflight) {
		if structuredOutput(stdout, jsonOutput) {
			if code := encodeJSON(stdout, stderr, networkPreflight.Report); code != exitOK {
				return code
			}
		}
		errorf(stderr, "%v", networkPreflight)
		return networkPreflight.Report.ExitCode
	}
	var capability *privatevm.CapabilityError
	if errors.As(err, &capability) {
		return reportCommandFailure(stdout, stderr, jsonOutput, "capability", capability.Error(), operationID, exitCapability)
	}
	var partial *privatevm.PartialError
	if errors.As(err, &partial) {
		if structuredOutput(stdout, jsonOutput) {
			payload := lifecyclePartialFailure{Error: "partial", Message: partial.Error(), OperationID: operationID, Nodes: partial.Nodes, RolledBack: partial.RolledBack}
			if code := encodeJSON(stdout, stderr, payload); code != exitOK {
				return code
			}
		}
		errorf(stderr, "%v", partial)
		return exitPartial
	}
	var leaseConflict *lease.ConflictError
	if errors.As(err, &leaseConflict) {
		return reportCommandFailure(stdout, stderr, jsonOutput, "lease_conflict", leaseConflict.Error(), operationID, exitLease)
	}
	var deleteErr *persistentDeleteError
	if errors.As(err, &deleteErr) {
		return reportCommandFailure(stdout, stderr, jsonOutput, "persistent_delete", deleteErr.Error(), operationID, exitIntegrity)
	}
	return reportCommandFailure(stdout, stderr, jsonOutput, "runtime", err.Error(), operationID, exitRuntime)
}

func runPrivateCommand(command string, resolved spec.Resolved, nodes []string, repository string, force, deletePersistent, purge, noWait, rollback, jsonOutput bool, stdout, stderr io.Writer) int {
	operationID := ""
	if command != "status" && command != "plan" {
		var err error
		operationID, err = identity.NewUUID()
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
	}
	var progressItem *progress
	manager := privatevm.Manager{FarrowVersion: version.Version, OperationID: operationID, Repository: repository, NoWait: noWait, RollbackFailed: rollback, Nodes: append([]string(nil), nodes...)}
	manager.Progress = deferredProgressReporter(&progressItem)
	if command == "plan" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		plan, err := manager.Plan(ctx, resolved)
		if err != nil {
			return reportPrivateLifecycleError(err, operationID, jsonOutput, stdout, stderr)
		}
		if structuredOutput(stdout, jsonOutput) {
			return encodeJSON(stdout, stderr, plan)
		}
		textField(stdout, 14, "action", statusValue(stdout, plan.Action))
		textField(stdout, 14, "destructive", plan.Destructive)
		textField(stdout, 14, "spec hash", plan.SpecHash)
		textField(stdout, 14, "nodes", strings.Join(plan.Nodes, ","))
		if len(plan.Create) != 0 {
			textField(stdout, 14, "create", strings.Join(plan.Create, ","))
		}
		if len(plan.Recreate) != 0 {
			textField(stdout, 14, "recreate", strings.Join(plan.Recreate, ",")+"  (apply: farrow recreate --force "+strings.Join(plan.Recreate, " ")+")")
		}
		if len(plan.Missing) != 0 {
			textField(stdout, 14, "missing", strings.Join(plan.Missing, ",")+"  (config dropped them; remove with: farrow destroy "+strings.Join(plan.Missing, " ")+" --force)")
		}
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
			errorf(stderr, "%v", err)
			return exitUsage
		}
		timeout = withReadinessTimeout(20*time.Minute, resolved)
		operation = func(ctx context.Context) (privatevm.Status, error) { return manager.RecreateResolved(ctx, resolved) }
	case "status":
		operation = manager.Status
	case "destroy":
		action := "private destroy"
		if len(nodes) != 0 {
			action = "private node destroy"
			if deletePersistent || purge {
				fmt.Fprintln(stderr, "--delete-persistent and --purge apply to whole-project destroy only")
				return exitUsage
			}
		}
		if err := confirmCLIAction(force, action, stderr); err != nil {
			errorf(stderr, "%v", err)
			return exitUsage
		}
		timeout = 10 * time.Minute
		operation = func(ctx context.Context) (privatevm.Status, error) {
			if len(nodes) != 0 {
				return manager.DestroyNodes(ctx)
			}
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
	debugf(stderr, "lifecycle=%s mode=private timeout=%s operation_id=%s nodes=%d", command, timeout, operationID, len(resolved.Nodes))
	if command != "status" {
		progressItem = startProgress(ctx, stderr, lifecycleMessage(command))
	}
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
	progressItem.Stop(err)
	if err != nil {
		return reportPrivateLifecycleError(err, operationID, jsonOutput, stdout, stderr)
	}
	if command == "destroy" && purge {
		if purgeErr := purgeDeployment(ctx, stdout); purgeErr != nil {
			errorf(stderr, "destroy succeeded but purge failed: %v", purgeErr)
			return exitIntegrity
		}
	}
	if command == "up" {
		suggestHostsPublication(resolved, stderr)
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
			if service, serviceErr := imageService(repository, nil); serviceErr == nil {
				if info, infoErr := service.Info(ctx, alias); infoErr == nil {
					printImageStatusWarning(stderr, info.Entry)
				}
			}
		}
	}
	if structuredOutput(stdout, jsonOutput) {
		return encodeJSON(stdout, stderr, status)
	}
	printPrivateStatus(stdout, status)
	return exitOK
}

func runLifecycleCommand(command string, options lifecycleOptions, nodes []string, stdout, stderr io.Writer) int {
	if (options.DeletePersistent || options.Purge) && !options.Force {
		return reportCommandFailure(stdout, stderr, false, "usage", "--delete-persistent and --purge require the separate --force destroy confirmation", "", exitUsage)
	}
	if options.Purge {
		// A purge is a terminal disposal; retained persistent disks make no
		// sense past it.
		options.DeletePersistent = true
	}
	resolvedFile, hasConfig, err := loadLifecycleConfig(command, options.ConfigPath)
	if err != nil {
		return reportCommandFailure(stdout, stderr, false, "usage", err.Error(), "", exitUsage)
	}
	persisted, persistedErr := currentProjectResolved()
	switch {
	case persistedErr == nil && persisted.Network == "private":
		if !hasConfig {
			resolvedFile = persisted
			hasConfig = true
		}
	case persistedErr == nil && persisted.Network == "user":
		return reportCommandFailure(stdout, stderr, false, "conflict", "this project predates the fixed-IP redesign; remove it with `rm -rf .farrow` (plus any pre-redesign binary for running VMs) and run `farrow up` again", "", exitConflict)
	}
	if !hasConfig {
		return reportCommandFailure(stdout, stderr, false, "usage", config.ErrNoConfig.Error(), "", exitConflict)
	}
	printWarnings(stderr, configurationWarnings(resolvedFile))
	if command == "reload" {
		// Vagrant-style reload: a stop/boot cycle that re-reads the
		// configuration. Additive changes apply on the way up; destructive
		// ones report their per-node recreate path exactly like plain up.
		if code := runPrivateCommand("stop", resolvedFile, nodes, options.Repository, options.Force, false, false, options.NoWait, false, false, stdout, stderr); code != exitOK {
			return code
		}
		command = "up"
	}
	return runPrivateCommand(command, resolvedFile, nodes, options.Repository, options.Force, options.DeletePersistent, options.Purge, options.NoWait, options.Rollback, false, stdout, stderr)
}

func runSSHConfig(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("ssh-config", stderr)
	install := flags.Bool("install", false, "install a marker-owned Include and project fragment")
	remove := flags.Bool("remove", false, "remove only this project's marker-owned Include and fragment")
	name := flags.String("name", "farrow", "safe SSH Host/file prefix")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || (*install && *remove) {
		fmt.Fprintln(stderr, "usage: farrow ssh-config [--install|--remove] [--name name] [--json|--yaml] [--verbose] [node...]")
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
		result, err := removeSSHConfigFragment(*name)
		if err != nil {
			return reportSSHConfigFailure(result, err, *jsonOutput, stdout, stderr)
		}
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, result)
		}
		fmt.Fprintf(stdout, "%s SSH config %s (changed=%t)\nfragment: %s\nconfig: %s\n", result.Action, *name, result.Changed, result.Fragment, result.Config)
		return exitOK
	}
	if resolved, err := currentProjectResolved(); err == nil && resolved.Network == "private" {
		manager := privatevm.Manager{FarrowVersion: version.Version, Nodes: append([]string(nil), nodes...)}
		if *install {
			var result sshconfig.Result
			result, err = manager.InstallSSHConfig(ctx, *name, "")
			if err != nil {
				return reportSSHConfigFailure(result, err, *jsonOutput, stdout, stderr)
			}
			if structuredOutput(stdout, *jsonOutput) {
				return encodeJSON(stdout, stderr, result)
			}
			fmt.Fprintf(stdout, "%s SSH config %s (changed=%t)\nfragment: %s\nconfig: %s\n", result.Action, *name, result.Changed, result.Fragment, result.Config)
			return exitOK
		}
		text, err := manager.SSHConfig(ctx)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, map[string]string{"config": text})
		}
		fmt.Fprint(stdout, text)
		return exitOK
	}
	errorf(stderr, "no deployment state found; run `farrow up` first")
	return exitConflict
}

// removeSSHConfigFragment is deliberately state-independent: destroy keeps
// the fragment on disk, and sshconfig.Remove itself deletes only the exact
// marker-owned fragment and Include line.
func removeSSHConfigFragment(name string) (sshconfig.Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sshconfig.Result{}, err
	}
	return sshconfig.Remove(home, name)
}

func reportSSHConfigFailure(result sshconfig.Result, err error, jsonOutput bool, stdout, stderr io.Writer) int {
	partial := result.Changed && strings.HasSuffix(result.Action, "-partial")
	if structuredOutput(stdout, jsonOutput) {
		payload := struct {
			Error   string           `json:"error"`
			Message string           `json:"message"`
			Partial bool             `json:"partial"`
			Result  sshconfig.Result `json:"result"`
		}{Error: "ssh_config", Message: err.Error(), Partial: partial, Result: result}
		if code := encodeJSON(stdout, stderr, payload); code != exitOK {
			return code
		}
	}
	if partial {
		errorf(stderr, "SSH config operation partially changed owned state; retry is safe (action=%s fragment=%s config=%s): %v", result.Action, result.Fragment, result.Config, err)
	} else {
		errorf(stderr, "%v", err)
	}
	return exitIntegrity
}

func runHosts(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != hostconfig.ActionInstall && args[0] != hostconfig.ActionUninstall) {
		fmt.Fprintln(stderr, "usage: farrow hosts install|uninstall [--json|--yaml] [--verbose] [--yes]")
		return exitUsage
	}
	action := args[0]
	flags := newCommandFlagSet("hosts "+action, stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	apply := flags.Bool("yes", false, "apply the displayed privileged plan")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	manager := privatevm.Manager{FarrowVersion: version.Version}
	var entries []hostconfig.Entry
	var err error
	if action == hostconfig.ActionInstall {
		entries, err = manager.HostEntries(ctx)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitCapability
		}
	}
	baseRunner := execx.OSRunner{Timeout: 30 * time.Second, OutputLimit: 1 << 20}
	debugf(stderr, "hosts action=%s entries=%d apply=%t", action, len(entries), *apply)
	progressMessage := "Installing deployment host entries"
	if action == hostconfig.ActionUninstall {
		progressMessage = "Removing deployment host entries"
	}
	progressItem := startProgress(ctx, stderr, progressMessage)
	report, err := (hostconfig.Executor{Root: sudoRunner{base: baseRunner}}).Execute(ctx, action, entries, *apply)
	progressItem.Stop(err)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitIntegrity
	}
	if structuredOutput(stdout, *jsonOutput) {
		return encodeJSON(stdout, stderr, report)
	}
	textField(stdout, 16, "action", statusValue(stdout, report.Plan.Action))
	textField(stdout, 16, "target", report.Plan.Target)
	textField(stdout, 16, "changed", report.Plan.Changed)
	textField(stdout, 16, "applied", report.Applied)
	textField(stdout, 16, "before sha256", report.Plan.BeforeSHA256)
	textField(stdout, 16, "after sha256", report.Plan.AfterSHA256)
	textField(stdout, 16, "helper", report.Plan.HelperPath)
	textField(stdout, 16, "helper sha256", report.Plan.HelperSHA256)
	for _, line := range report.Plan.Lines {
		fmt.Fprintln(stdout, line)
	}
	if report.Plan.Changed && !report.Applied {
		fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact marker-owned plan and caching sudo credentials")
	}
	return exitOK
}

type logResult struct {
	Node       string `json:"node"`
	Source     string `json:"source"`
	Path       string `json:"path"`
	Content    string `json:"content"`
	Bytes      int    `json:"bytes"`
	TotalBytes int64  `json:"total_bytes"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type logStreamRecord struct {
	Type      string `json:"type"`
	Node      string `json:"node"`
	Source    string `json:"source"`
	Path      string `json:"path"`
	Sequence  uint64 `json:"sequence,omitempty"`
	Content   string `json:"content,omitempty"`
	Continued bool   `json:"continued,omitempty"`
}

const structuredLogRecordLimit = 64 << 10

func readStructuredLogChunk(reader *bufio.Reader) (string, bool, error) {
	chunk, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return string(chunk), true, nil
	}
	return string(chunk), false, err
}

func splitLogArgs(args []string) (string, []string, error) {
	node := ""
	flagArgs := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--source" || argument == "-source":
			if index+1 >= len(args) {
				return "", nil, fmt.Errorf("%s requires a value", argument)
			}
			flagArgs = append(flagArgs, argument, args[index+1])
			index++
		case strings.HasPrefix(argument, "--source=") || strings.HasPrefix(argument, "-source=") || argument == "--follow" || argument == "-follow" || strings.HasPrefix(argument, "--follow=") || strings.HasPrefix(argument, "-follow="):
			flagArgs = append(flagArgs, argument)
		case strings.HasPrefix(argument, "-"):
			flagArgs = append(flagArgs, argument)
		default:
			if node != "" {
				return "", nil, fmt.Errorf("logs accepts one node, got %q and %q", node, argument)
			}
			node = argument
		}
	}
	return node, flagArgs, nil
}

func runLogs(args []string, stdout, stderr io.Writer) int {
	requestedNode, flagArgs, splitErr := splitLogArgs(args)
	if splitErr != nil {
		errorf(stderr, "%v", splitErr)
		return exitUsage
	}
	flags := newCommandFlagSet("logs", stderr)
	source := flags.String("source", "serial", "serial, qemu, or events")
	follow := flags.Bool("follow", false, "continue streaming appended log data")
	if err := flags.Parse(flagArgs); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: farrow logs [node] [--source serial|qemu|events] [--follow]")
		return exitUsage
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolved, resolveErr := currentProjectResolved()
	if resolveErr != nil || resolved.Network != "private" {
		errorf(stderr, "no deployment state found; run `farrow up` first")
		return exitConflict
	}
	node := requestedNode
	path, err := (privatevm.Manager{FarrowVersion: version.Version}).LogPath(node, *source)
	if node == "" {
		if *source == "events" {
			node = "project"
		} else {
			for _, candidate := range resolved.Nodes {
				if candidate.Control {
					node = candidate.Name
					break
				}
			}
		}
	}
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	handle, err := os.Open(path)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	defer handle.Close()
	if structuredOutput(stdout, false) && !*follow {
		info, err := handle.Stat()
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		capture := boundedCapture{limit: structuredCommandCaptureLimit}
		if _, err := io.Copy(&capture, handle); err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		content := capture.String()
		return encodeJSON(stdout, stderr, logResult{
			Node: node, Source: *source, Path: path, Content: content,
			Bytes: len(content), TotalBytes: info.Size(), Truncated: capture.truncated,
		})
	}
	if structuredOutput(stdout, false) {
		if err := encodeStreamOutput(stdout, logStreamRecord{Type: "start", Node: node, Source: *source, Path: path}); err != nil {
			fmt.Fprintf(stderr, "encode %s log stream: %v\n", outputFormatFor(stdout), err)
			return exitRuntime
		}
		reader := bufio.NewReaderSize(handle, structuredLogRecordLimit)
		var sequence uint64
		for {
			chunk, continued, readErr := readStructuredLogChunk(reader)
			if chunk != "" {
				sequence++
				if err := encodeStreamOutput(stdout, logStreamRecord{
					Type: "line", Node: node, Source: *source, Path: path, Sequence: sequence,
					Content: chunk, Continued: continued,
				}); err != nil {
					fmt.Fprintf(stderr, "encode %s log stream: %v\n", outputFormatFor(stdout), err)
					return exitRuntime
				}
			}
			if readErr == nil {
				continue
			}
			if !errors.Is(readErr, io.EOF) {
				errorf(stderr, "%v", readErr)
				return exitRuntime
			}
			select {
			case <-ctx.Done():
				return exitOK
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if _, err := io.Copy(stdout, handle); err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	if *follow {
		fmt.Fprintf(stderr, "%s following %s log for %s\n", styled(stderr, ansiCyan, "→"), *source, node)
	}
	for *follow {
		select {
		case <-ctx.Done():
			return exitOK
		case <-time.After(250 * time.Millisecond):
			if _, err := io.Copy(stdout, handle); err != nil {
				errorf(stderr, "%v", err)
				return exitRuntime
			}
		}
	}
	return exitOK
}

func runRepair(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("repair", stderr)
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
		progressItem := startProgress(ctx, stderr, "Inspecting and repairing the private project")
		report, err := (privatevm.Manager{FarrowVersion: version.Version, Nodes: append([]string(nil), nodes...)}).Repair(ctx, *force)
		progressItem.Stop(err)
		if structuredOutput(stdout, *jsonOutput) {
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
			errorf(stderr, "%v", err)
			return exitIntegrity
		}
		return exitOK
	}
	errorf(stderr, "no deployment state found; run `farrow up` first")
	return exitConflict
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("validate", stderr)
	filePath := flags.String("f", "", "configuration file")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	cwd, err := os.Getwd()
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	file, source, err := config.Discover(cwd, *filePath)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	resolved, err := file.Resolve()
	if err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	resolvedRoot, rootErr := state.ResolveDataRoot()
	if rootErr != nil {
		errorf(stderr, "%v", rootErr)
		return exitUsage
	}
	resolved.DataRoot = resolvedRoot
	hash, err := spec.Hash(resolved)
	if err != nil {
		errorf(stderr, "%v", err)
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
	if structuredOutput(stdout, *jsonOutput) {
		return encodeJSON(stdout, stderr, result)
	}
	printWarnings(stderr, warnings)
	textField(stdout, 12, "status", statusValue(stdout, "valid"))
	textField(stdout, 12, "source", source)
	textField(stdout, 12, "spec hash", hash)
	return exitOK
}

func runInit(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("init", stderr)
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	networkCIDR := flags.String("network-cidr", "", "rebase the template to one RFC1918 IPv4 /24")
	output := flags.String("output", "", "write to this path instead of ./pigsty.yml; '-' writes to stdout")
	force := flags.Bool("force", false, "overwrite an existing configuration file")
	if err := flags.Parse(args); err != nil || flags.NArg() > 1 {
		fmt.Fprintf(stderr, "usage: farrow init [%s] [--network-cidr RFC1918/24] [-o path|-] [--force]\n", strings.Join(config.TemplateNames(), "|"))
		return exitUsage
	}
	name := "meta"
	if flags.NArg() == 1 {
		name = flags.Arg(0)
	}
	data, err := config.Template(name, *networkCIDR)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	if *networkCIDR != "" {
		if layout, parseErr := subnet.Parse(*networkCIDR); parseErr == nil {
			if warning := layout.Warning(); warning != "" {
				warningf(stderr, "%s", warning)
			}
		}
	}
	if *output == "-" {
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, struct {
				Template string `json:"template"`
				Content  string `json:"content"`
			}{name, string(data)})
		}
		_, _ = stdout.Write(data)
		return exitOK
	}
	target := *output
	if target == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			errorf(stderr, "%v", cwdErr)
			return exitRuntime
		}
		target = filepath.Join(cwd, "pigsty.yml")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	if *force {
		err = fsutil.AtomicWrite(target, data, 0o600)
	} else {
		err = fsutil.AtomicCreate(target, data, 0o600)
	}
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			errorf(stderr, "%s already exists; edit it, pass --force to replace it, or -o for another path", target)
			return exitConflict
		}
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	if structuredOutput(stdout, *jsonOutput) {
		return encodeJSON(stdout, stderr, struct {
			Template string `json:"template"`
			Path     string `json:"path"`
		}{name, target})
	}
	textField(stdout, 10, "template", name)
	textField(stdout, 10, "wrote", target)
	textField(stdout, 10, "next", "farrow setup && farrow up")
	return exitOK
}

func runImage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: farrow image list|info|pull|prune|import|sync|reset-manifest")
		return exitUsage
	}
	switch args[0] {
	case "list":
		flags := newCommandFlagSet("image list", stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		repository := flags.String("repo", "", "image repository URL or absolute local directory")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		service, err := imageService(*repository, nil)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		entries, manifestState, err := service.List(ctx)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, struct {
				Manifest image.ManifestState `json:"manifest"`
				Images   []image.Entry       `json:"images"`
			}{manifestState, entries})
		}
		textField(stdout, 12, "manifest", manifestState.Active)
		textField(stdout, 12, "version", manifestState.ActiveVersion)
		textField(stdout, 12, "highest", manifestState.HighestVersion)
		for _, entry := range entries {
			fmt.Fprintf(stdout, "%s %s %s %s %s\n", entry.Alias, entry.Release, entry.Arch, entry.Status, entry.SHA256)
		}
		return exitOK
	case "info", "pull":
		flags := newCommandFlagSet("image "+args[0], stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		repository := flags.String("repo", "", "image repository URL or absolute local directory")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 1 {
			fmt.Fprintf(stderr, "usage: farrow image %s [--repo URL|DIR] [--json|--yaml] [--verbose] <alias>\n", args[0])
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		var progressItem *progress
		service, err := imageService(*repository, deferredProgressReporter(&progressItem))
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		var info image.Info
		if args[0] == "pull" {
			debugf(stderr, "image pull alias=%s timeout=%s", flags.Arg(0), 30*time.Minute)
			progressItem = startProgress(ctx, stderr, fmt.Sprintf("Pulling image %s", flags.Arg(0)))
			info, err = service.PullAlias(ctx, flags.Arg(0))
		} else {
			info, err = service.Info(ctx, flags.Arg(0))
		}
		progressItem.Stop(err)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		printImageStatusWarning(stderr, info.Entry)
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, info)
		}
		fmt.Fprintf(stdout, "%s %s %s status=%s sha256:%s cached=%t\n", info.Entry.Alias, info.Entry.Release, info.Entry.Arch, info.Entry.Status, info.Entry.SHA256, info.Cached)
		if info.Path != "" {
			fmt.Fprintf(stdout, "path: %s\n", info.Path)
		}
		return exitOK
	case "prune":
		flags := newCommandFlagSet("image prune", stderr)
		dryRun := flags.Bool("dry-run", false, "show unreferenced images and stale staging files without deleting")
		yes := flags.Bool("yes", false, "delete the displayed unreferenced images and stale staging files")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*dryRun && *yes) {
			fmt.Fprintln(stderr, "usage: farrow image prune [--dry-run|--yes] [--json|--yaml] [--verbose]")
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		service, err := imageService("", nil)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		progressItem := startProgress(ctx, stderr, "Scanning unreferenced image cache entries")
		report, err := service.PruneAll(ctx, *yes, func(refCtx context.Context) (map[string]struct{}, error) {
			return nodeImageReferences(service.DataRoot)
		})
		progressItem.Stop(err)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitIntegrity
		}
		if structuredOutput(stdout, *jsonOutput) {
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
			if item.Digest == "" {
				fmt.Fprintf(stdout, "%s %s (%d bytes, %s)\n", action, item.ImagePath, item.Bytes, item.Kind)
			} else {
				fmt.Fprintf(stdout, "%s %s sha256:%s (%d bytes)\n", action, item.ImagePath, item.Digest, item.Bytes)
			}
		}
		return exitOK
	case "sync":
		flags := newCommandFlagSet("image sync", stderr)
		allowDowngrade := flags.Bool("allow-downgrade", false, "explicitly activate a version below the high-water mark")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: farrow image sync [--allow-downgrade] <url|path>")
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		debugf(stderr, "image manifest sync source=%s allow_downgrade=%t", flags.Arg(0), *allowDowngrade)
		service, err := imageService("", nil)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		progressItem := startProgress(ctx, stderr, "Synchronizing the image manifest")
		state, err := service.SyncManifest(ctx, flags.Arg(0), *allowDowngrade)
		progressItem.Stop(err)
		if err != nil {
			_ = emitCommandFailure(stdout, stderr, *jsonOutput, "runtime", err.Error(), "")
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, state)
		}
		fmt.Fprintf(stdout, "activated manifest version %d digest %s key %s\n", state.ActiveVersion, state.ActiveDigest, state.KeyID)
		return exitOK
	case "reset-manifest":
		flags := newCommandFlagSet("image reset-manifest", stderr)
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		service, err := imageService("", nil)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		state, err := service.ResetManifest(ctx)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		if structuredOutput(stdout, *jsonOutput) {
			return encodeJSON(stdout, stderr, state)
		}
		fmt.Fprintf(stdout, "active manifest reset to embedded version %d; high-water mark %d preserved\n", state.ActiveVersion, state.HighestVersion)
		return exitOK
	case "import":
		flags := newCommandFlagSet("image import", stderr)
		expected := flags.String("sha256", "", "optional expected SHA-256")
		name := flags.String("name", "", "optional immutable local- prefixed alias")
		boot := flags.String("boot", "", "required with --name: bios or uefi")
		sourceUser := flags.String("source-user", "", "required with --name: source image login user")
		jsonOutput := flags.Bool("json", false, "emit stable JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: farrow image import [--sha256 digest] [--name alias --boot bios|uefi --source-user user] <path>")
			return exitUsage
		}
		invalidAliasMetadata := (*name == "" && (*boot != "" || *sourceUser != "")) || (*name != "" && (*sourceUser == "" || (*boot != "bios" && *boot != "uefi")))
		if invalidAliasMetadata {
			fmt.Fprintln(stderr, "--name requires --boot bios|uefi and --source-user; alias metadata is immutable")
			return exitUsage
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		service, err := imageService("", nil)
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		var path string
		var metadata image.Metadata
		var localEntry *image.Entry
		debugf(stderr, "image import source=%s expected_sha256=%s alias=%s", flags.Arg(0), *expected, *name)
		progressItem := startProgress(ctx, stderr, "Importing and verifying the image")
		if *name == "" {
			path, metadata, err = service.ImportFile(ctx, flags.Arg(0), *expected)
		} else {
			entry, importedPath, importedMetadata, importErr := service.ImportNamed(ctx, flags.Arg(0), *expected, *name, *boot, *sourceUser)
			path, metadata, err = importedPath, importedMetadata, importErr
			if importErr == nil {
				localEntry = &entry
			}
		}
		progressItem.Stop(err)
		result := struct {
			Path     string         `json:"path"`
			Metadata image.Metadata `json:"metadata"`
			Entry    *image.Entry   `json:"entry,omitempty"`
		}{Path: path, Metadata: metadata, Entry: localEntry}
		if structuredOutput(stdout, *jsonOutput) {
			if code := encodeJSON(stdout, stderr, result); code != exitOK {
				return code
			}
		} else if path != "" {
			fmt.Fprintf(stdout, "imported %s\nsha256 %s\n", path, metadata.Digest)
		}
		if err != nil {
			errorf(stderr, "%v", err)
			return exitRuntime
		}
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown image command %q\n", args[0])
		return exitUsage
	}
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("doctor", stderr)
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
	progressItem := startProgress(ctx, stderr, "Checking host capabilities")
	report := (doctor.Probe{}).Run(ctx)
	progressItem.Stop(nil)
	if structuredOutput(stdout, *jsonOutput) {
		if code := encodeJSON(stdout, stderr, report); code != exitOK {
			return code
		}
	} else {
		textField(stdout, 10, "host", fmt.Sprintf("%s/%s", report.OS, report.Arch))
		textField(stdout, 10, "tier", report.Tier)
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "%s %-20s %s\n", statusCell(stdout, 10, string(check.Status)), check.Name, check.Evidence)
			if check.Fix != "" {
				fmt.Fprintf(stdout, "  fix: %s\n", check.Fix)
			}
		}
		if !report.NetworkReady() {
			fmt.Fprintln(stdout, "the host-global network is not ready; run `farrow setup` to prepare it")
		}
	}
	if report.HasErrors() {
		return exitCapability
	}
	return exitOK
}

func run(args []string, stdout, stderr io.Writer) int {
	prepared, preparedStdout, preparedStderr, err := prepareOutput(args, stdout, stderr)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	if len(prepared) > 0 {
		debugf(preparedStderr, "command=%s format=%s stdout_tty=%t stderr_tty=%t", prepared[0], outputFormatFor(preparedStdout), writerTTY(preparedStdout), writerTTY(preparedStderr))
	}
	return executeCLI(prepared, preparedStdout, preparedStderr)
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// suggestHostsPublication reminds the user once per up that declared host
// aliases are reachable by name only after `farrow hosts install`. Best
// effort: any read problem stays silent.
func suggestHostsPublication(resolved spec.Resolved, stderr io.Writer) {
	hasAliases := false
	for _, node := range resolved.Nodes {
		if len(node.Aliases) != 0 {
			hasAliases = true
		}
	}
	if !hasAliases {
		return
	}
	target, err := hostconfig.NativePath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(target)
	if err != nil || len(data) > 1<<20 {
		return
	}
	if strings.Contains(string(data), "# farrow:begin") {
		return
	}
	fmt.Fprintln(stderr, "declared host aliases are not published; run `farrow hosts install --yes` to add them to "+target)
}

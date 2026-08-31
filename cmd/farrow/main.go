package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/config"
	"github.com/pgsty/farrow/internal/doctor"
	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/hostconfig"
	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/image"
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
	exitResource   = 6
	exitIntegrity  = 7
	exitCancelled  = 130

	// Compatibility expiry: user-network-state-v0 in CONTRIBUTING.md#compatibility-expiry.
	legacyDeploymentMessage = "this deployment predates the fixed-IP redesign; preserve any needed disks, then move or remove the selected FARROW_HOME and run `farrow setup && farrow up`"
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
				warnings = append(warnings, fmt.Sprintf("node %s exposes host TCP %s:%d beyond loopback; this may make guest port %d reachable from other machines", node.Name, forward.Bind, forward.Host, forward.Guest))
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
	if entry.Status == "deprecated" {
		warningf(out, "image %s/%s (%s) is deprecated and EOL; use only for isolated compatibility testing", entry.Alias, entry.Arch, entry.Release)
	} else if entry.Status != "" && entry.Status != "supported" {
		warningf(out, "image %s/%s (%s) has status %s, not supported; use only with the corresponding test/risk acceptance", entry.Alias, entry.Arch, entry.Release, entry.Status)
	}
}

func lifecycleImageArch(resolved spec.Resolved) string {
	if resolved.Arch != "" {
		return resolved.Arch
	}
	return runtime.GOARCH
}

func confirmDestructive(force, interactive bool, action string, input io.Reader, output io.Writer) error {
	if force {
		return nil
	}
	if !interactive {
		return fmt.Errorf("%s requires --force when stdin is not a TTY", action)
	}
	if _, err := fmt.Fprintf(output, "Confirm scoped Farrow %s by typing %q: ", action, action); err != nil {
		return fmt.Errorf("write %s confirmation prompt: %w", action, err)
	}
	line, err := bufio.NewReader(io.LimitReader(input, 256)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read %s confirmation: %w", action, err)
	}
	if strings.TrimSpace(line) != action {
		return fmt.Errorf("%w: %s confirmation did not match %q", ErrCancelled, action, action)
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

type sudoRunner struct {
	base                execx.Runner
	preserveEnvironment []string
}

func (r sudoRunner) Run(ctx context.Context, binary string, args ...string) (execx.Result, error) {
	if r.base == nil {
		return execx.Result{}, errors.New("sudo runner has no base runner")
	}
	sudoArgs := []string{"-n"}
	if len(r.preserveEnvironment) > 0 {
		sudoArgs = append(sudoArgs, "--preserve-env="+strings.Join(r.preserveEnvironment, ","))
	}
	sudoArgs = append(sudoArgs, "--", binary)
	sudoArgs = append(sudoArgs, args...)
	return r.base.Run(ctx, "/usr/bin/sudo", sudoArgs...)
}

type darwinNetworkInstaller interface {
	InstallModeNetwork(context.Context, string, string, string, string, string, bool) (darwinnet.InstallReport, error)
}

func installDarwinNetwork(ctx context.Context, installer darwinNetworkInstaller, archive, interfaceID, arch, mode, cidr string, apply bool) (darwinnet.InstallReport, error) {
	return installer.InstallModeNetwork(ctx, archive, interfaceID, arch, mode, cidr, apply)
}

type networkOptions struct {
	Action      string
	CIDR        string
	Mode        string
	Archive     string
	InterfaceID string
	Apply       bool
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func runNetwork(parent context.Context, options networkOptions, stderr io.Writer) (commandOutcome, error) {
	command := options.Action
	if command == "install" || command == "uninstall" {
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			return commandOutcome{}, newDetailedCommandError("capability", exitCapability, errors.New("network install/uninstall supports native Linux and macOS"), "", nil)
		}
		var layout subnet.Layout
		vmnetMode := "host"
		if command == "install" {
			vmnetMode = options.Mode
			if vmnetMode == "" {
				vmnetMode = "host"
			}
			if vmnetMode != "host" && vmnetMode != "shared" {
				return commandOutcome{}, newUsageError(fmt.Errorf("--mode must be host or shared, got %q", vmnetMode))
			}
			if runtime.GOOS == "linux" && vmnetMode != "host" {
				return commandOutcome{}, newUsageError(errors.New("--mode is a macOS socket_vmnet option; Linux uses farrow0"))
			}
			if runtime.GOOS != "darwin" && (options.Archive != "" || options.InterfaceID != "") {
				return commandOutcome{}, newUsageError(errors.New("--archive and --interface-id are macOS-only"))
			}
			networkCIDR := options.CIDR
			if networkCIDR == "" {
				networkCIDR = subnet.DefaultCIDR
			}
			var layoutErr error
			layout, layoutErr = subnet.Parse(networkCIDR)
			if layoutErr != nil {
				return commandOutcome{}, newUsageError(layoutErr)
			}
		}
		baseRunner := execx.OSRunner{Timeout: 2 * time.Minute, OutputLimit: 1 << 20}
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		if command == "install" {
			addresses := withoutRecordedAddresses(layout.StaticAddresses())
			preflightProgress := startProgress(ctx, stderr, "Validating the fixed-IP network plan")
			preflightReport := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Install, Layout: layout, Addresses: addresses}, netpreflight.Probe{Runner: baseRunner})
			preflightProgress.Stop(nil)
			if errors.Is(parent.Err(), context.Canceled) {
				return commandOutcome{}, ErrCancelled
			}
			if !preflightReport.Ready {
				err := errors.New("host network preflight is not ready")
				return commandOutcome{}, newSilentRenderedCommandError("network_preflight", preflightReport.ExitCode, err, preflightReport, func(_ io.Writer, stderr io.Writer) error {
					for _, finding := range preflightReport.Findings {
						if _, writeErr := fmt.Fprintf(stderr, "%s %s: %s\n", finding.Severity, finding.Code, finding.Evidence); writeErr != nil {
							return writeErr
						}
					}
					return nil
				})
			}
		}
		privilege := &sudoSession{base: baseRunner, stderr: stderr, scope: "network command"}
		defer privilege.close()
		reason := "read the protected host-network ownership state for the plan"
		if options.Apply {
			reason = "apply the reviewed host-network plan"
		}
		if err := privilege.ensure(ctx, reason); err != nil {
			return commandOutcome{}, newDetailedCommandError("capability", exitCapability, err, "", nil)
		}
		rootRunner := setupRootRunner(baseRunner)
		if runtime.GOOS == "darwin" {
			executor := darwinnet.Executor{User: baseRunner, Root: rootRunner, InUse: deploymentInUse}
			if command == "install" {
				progressItem := startProgress(ctx, stderr, "Installing the Darwin fixed-IP network")
				report, err := installDarwinNetwork(ctx, executor, options.Archive, options.InterfaceID, runtime.GOARCH, vmnetMode, layout.CIDR(), options.Apply)
				progressItem.Stop(err)
				if errors.Is(parent.Err(), context.Canceled) {
					return commandOutcome{}, ErrCancelled
				}
				if err != nil {
					if errors.Is(err, darwinnet.ErrVMNetSharingBusy) {
						return commandOutcome{}, newConflictError(err)
					}
					return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
				}
				for _, warning := range report.Warnings {
					warningf(stderr, "%s", warning)
				}
				return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
					if _, err := fmt.Fprintf(stdout, "action: %s\napplied: %t\n", report.Action, report.Applied); err != nil {
						return err
					}
					for _, path := range sortedMapKeys(report.Targets) {
						if _, err := fmt.Fprintf(stdout, "%s %s\n", path, report.Targets[path]); err != nil {
							return err
						}
					}
					if !report.Applied && report.Action != "none" {
						_, err := fmt.Fprintln(stdout, "rerun with --yes using the same --archive and --interface-id; Farrow will request sudo when needed")
						return err
					}
					return nil
				}}, nil
			}
			progressItem := startProgress(ctx, stderr, "Removing the Darwin fixed-IP network")
			report, err := executor.Uninstall(ctx, options.Apply)
			progressItem.Stop(err)
			if errors.Is(parent.Err(), context.Canceled) {
				return commandOutcome{}, ErrCancelled
			}
			if err != nil {
				return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
			}
			if report.Recovered {
				warningf(stderr, "protected network.json was unavailable; uninstall ownership was recovered from byte-identical interface evidence, the exact launchd plist, and installed binary digests")
			}
			return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
				for _, path := range report.RemoveFiles {
					if _, err := fmt.Fprintf(stdout, "remove file %s\n", path); err != nil {
						return err
					}
				}
				for _, path := range report.RemoveDirs {
					if _, err := fmt.Fprintf(stdout, "rmdir %s\n", path); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(stdout, "applied: %t\n", report.Applied)
				return err
			}}, nil
		}
		executor := linuxnet.Executor{User: baseRunner, Root: rootRunner, InUse: deploymentInUse}
		if command == "install" {
			linuxConfig, configErr := linuxnet.ConfigForCIDR(layout.CIDR())
			if configErr != nil {
				return commandOutcome{}, newUsageError(configErr)
			}
			progressItem := startProgress(ctx, stderr, "Installing the Linux fixed-IP network")
			report, err := executor.InstallConfig(ctx, linuxConfig, options.Apply)
			progressItem.Stop(err)
			if errors.Is(parent.Err(), context.Canceled) {
				return commandOutcome{}, ErrCancelled
			}
			if err != nil {
				return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
			}
			for _, warning := range report.Warnings {
				warningf(stderr, "%s", warning)
			}
			return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
				for _, directory := range report.Plan.Directories {
					if err := writeText(stdout, "directory %s %s %s\n", directory.Path, directory.Owner, directory.Mode); err != nil {
						return err
					}
				}
				for _, file := range report.Plan.Files {
					if err := writeText(stdout, "file %s %s %s\n", file.Path, file.Owner, file.Mode); err != nil {
						return err
					}
				}
				for _, phase := range report.Plan.Phases {
					if err := writeText(stdout, "phase %s\n", phase.Name); err != nil {
						return err
					}
					for _, action := range phase.Commands {
						if err := writeText(stdout, "  %s\n", execx.Display(action.Binary, action.Args...)); err != nil {
							return err
						}
					}
				}
				if err := writeText(stdout, "applied: %t\n", report.Applied); err != nil {
					return err
				}
				if !report.Applied {
					_, err := fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact plan; Farrow will request sudo when needed")
					return err
				}
				return nil
			}}, nil
		}
		progressItem := startProgress(ctx, stderr, "Removing the Linux fixed-IP network")
		report, err := executor.Uninstall(ctx, options.Apply)
		progressItem.Stop(err)
		if errors.Is(parent.Err(), context.Canceled) {
			return commandOutcome{}, ErrCancelled
		}
		if err != nil {
			return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
		}
		return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
			for _, path := range report.Plan.RemoveFiles {
				if err := writeText(stdout, "remove file %s\n", path); err != nil {
					return err
				}
			}
			for _, directory := range report.Plan.RemoveDirectories {
				if err := writeText(stdout, "rmdir %s\n", directory); err != nil {
					return err
				}
			}
			if err := writeText(stdout, "applied: %t\n", report.Applied); err != nil {
				return err
			}
			if !report.Applied {
				_, err := fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact plan; Farrow will request sudo when needed")
				return err
			}
			return nil
		}}, nil
	}
	if command != "status" {
		return commandOutcome{}, newUsageError(fmt.Errorf("unknown network action %q", command))
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	cidr := options.CIDR
	if cidr == "" {
		cidr = subnet.DefaultCIDR
	}
	layout, err := subnet.Parse(cidr)
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	baseRunner := execx.OSRunner{Timeout: 5 * time.Second, OutputLimit: 1 << 20}
	progressItem := startProgress(ctx, stderr, "Inspecting the host network")
	preflightReport := netpreflight.Run(ctx, netpreflight.Request{OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Inspect, Layout: layout}, netpreflight.Probe{Runner: baseRunner})
	installedReadable := preflightReport.Installation.Status == "exact" || preflightReport.Installation.Status == "protected"
	if options.CIDR == "" && installedReadable && preflightReport.Installation.CIDR != "" && preflightReport.Installation.CIDR != layout.CIDR() {
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
	progressItem.Stop(nil)
	if errors.Is(parent.Err(), context.Canceled) {
		return commandOutcome{}, ErrCancelled
	}
	result := struct {
		OS        string              `json:"os"`
		Arch      string              `json:"arch"`
		Preflight netpreflight.Report `json:"preflight"`
		Checks    []doctor.Check      `json:"checks,omitempty"`
	}{OS: doctorReport.OS, Arch: doctorReport.Arch, Preflight: preflightReport, Checks: checks}
	renderText := func(stdout, _ io.Writer) error {
		if err := writeText(stdout, "network: %s ready=%t installation=%s\n", preflightReport.CIDR, preflightReport.Ready, preflightReport.Installation.Status); err != nil {
			return err
		}
		for _, finding := range preflightReport.Findings {
			if err := writeText(stdout, "[%s] %s: %s\n", finding.Severity, finding.Code, finding.Evidence); err != nil {
				return err
			}
		}
		for _, check := range checks {
			if err := writeText(stdout, "[%s] %s: %s\n", check.Status, check.Name, check.Evidence); err != nil {
				return err
			}
		}
		return nil
	}
	if hasError {
		code := exitCapability
		if preflightReport.ExitCode != 0 {
			code = preflightReport.ExitCode
		}
		return commandOutcome{}, newSilentRenderedCommandError(exitCategory(code), code, errors.New("network status is not ready"), result, renderText)
	}
	return commandOutcome{payload: result, text: renderText}, nil
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
	structured := structuredOutput(stdout)
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
	err := runSSHChild(sshCommand)
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

func runSSHChild(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	// Start first so the exec'd ssh process receives the terminal's normal
	// signal disposition. While it owns the foreground session, Farrow ignores
	// SIGINT: raw-mode Ctrl-C is an SSH byte, and non-raw Ctrl-C belongs to the
	// child process group rather than local command cancellation. SIGTERM still
	// reaches the command context and CommandContext terminates the child.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	return command.Wait()
}

// splitRemoteInvocation separates the optional node selector from the remote
// command. With a literal --, everything before it is the selector and must
// be nothing or one known node, so a misspelled node is a usage error rather
// than a command run on the control node. Without --, the first argument is
// the node only when it names one; anything else is the remote command.
func splitRemoteInvocation(arguments []string, resolved spec.Resolved) (string, []string, error) {
	known := func(name string) bool {
		for _, candidate := range resolved.Nodes {
			if candidate.Name == name {
				return true
			}
		}
		return false
	}
	for index, argument := range arguments {
		if argument != "--" {
			continue
		}
		head, command := arguments[:index], arguments[index+1:]
		switch {
		case len(head) == 0:
			return "", command, nil
		case len(head) > 1:
			return "", nil, fmt.Errorf("at most one node may precede --, got %s", strings.Join(head, " "))
		case !known(head[0]):
			return "", nil, fmt.Errorf("the deployment has no node %q", head[0])
		}
		return head[0], command, nil
	}
	if len(arguments) > 0 && known(arguments[0]) {
		return arguments[0], arguments[1:], nil
	}
	return "", arguments, nil
}

func runPrivateSSH(parent context.Context, commandName string, args []string, resolved spec.Resolved, stdout, stderr io.Writer) (commandOutcome, error) {
	node, command, err := splitRemoteInvocation(args, resolved)
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	if commandName == "exec" && len(command) == 0 {
		return commandOutcome{}, newUsageError(errors.New("exec requires a remote command: farrow exec [node] -- command [args...]"))
	}
	manager := privatevm.Manager{FarrowVersion: version.Version}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	connection, err := manager.Connection(ctx, node)
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return commandOutcome{}, newDetailedCommandError("capability", exitCapability, err, "", nil)
	}
	sshArgs := vm.SSHArgsForUser(connection.User, connection.PrivateKey, connection.KnownHosts, connection.Port, command...)
	if sshArgs == nil {
		return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, errors.New("resolved private SSH user is unsafe"), "", nil)
	}
	debugf(stderr, "ssh mode=private node=%s user=%s host=%s port=%d arguments=%d", connection.Node, connection.User, connection.Host, connection.Port, len(command))
	result, runErr := executeSSHProcess(ctx, commandName, connection.Node, connection.User, connection.Host, connection.Port, sshPath, sshArgs, command, stdout, stderr)
	if errors.Is(parent.Err(), context.Canceled) {
		return commandOutcome{}, ErrCancelled
	}
	outcome := commandOutcome{payload: result}
	if runErr != nil {
		var exitError *exec.ExitError
		if errors.As(runErr, &exitError) {
			code := exitError.ExitCode()
			if exitError.ExitCode() == 255 {
				code = exitRuntime
			}
			return commandOutcome{}, newRemoteExitError(code, result)
		}
		return commandOutcome{}, newRuntimeError(runErr)
	}
	return outcome, nil
}

func runSSH(ctx context.Context, commandName string, args []string, stdout, stderr io.Writer) (commandOutcome, error) {
	resolved, err := currentProjectResolved()
	if err != nil {
		return commandOutcome{}, newConflictError(errors.New("no deployment state found; run `farrow up` first"))
	}
	if resolved.Network != "private" {
		return commandOutcome{}, newConflictError(errors.New(legacyDeploymentMessage))
	}
	return runPrivateSSH(ctx, commandName, args, resolved, stdout, stderr)
}

func printProvisionReport(stdout, stderr io.Writer, report provision.Report) error {
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
		if err := writeText(stdout, "%-16s %s  exit=%d  duration=%dms\n", result.Node, statusValue(stdout, state), result.ExitCode, result.DurationMS); err != nil {
			return fmt.Errorf("write provision result: %w", err)
		}
		if result.Stdout != "" {
			if err := writeText(stdout, "--- %s stdout ---\n%s", result.Node, result.Stdout); err != nil {
				return fmt.Errorf("write provision stdout: %w", err)
			}
			if !strings.HasSuffix(result.Stdout, "\n") {
				if _, err := fmt.Fprintln(stdout); err != nil {
					return fmt.Errorf("write provision stdout separator: %w", err)
				}
			}
		}
		if result.Stderr != "" {
			if err := writeText(stderr, "--- %s stderr ---\n%s", result.Node, result.Stderr); err != nil {
				return fmt.Errorf("write provision stderr: %w", err)
			}
			if !strings.HasSuffix(result.Stderr, "\n") {
				if _, err := fmt.Fprintln(stderr); err != nil {
					return fmt.Errorf("write provision stderr separator: %w", err)
				}
			}
		}
		if result.StdoutTruncated {
			if err := writeText(stderr, "[%s] stdout was truncated at the bounded capture limit\n", result.Node); err != nil {
				return fmt.Errorf("write provision truncation warning: %w", err)
			}
		}
		if result.StderrTruncated {
			if err := writeText(stderr, "[%s] stderr was truncated at the bounded capture limit\n", result.Node); err != nil {
				return fmt.Errorf("write provision truncation warning: %w", err)
			}
		}
		if result.Error != "" {
			if err := writeText(stderr, "[%s] %s\n", result.Node, result.Error); err != nil {
				return fmt.Errorf("write provision error: %w", err)
			}
		}
	}
	return writeText(stdout, "%s %d successful, %d failed, %dms total\n", styled(stdout, ansiBold, "provisioned"), report.Successful, report.Failed, report.DurationMS)
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
func purgeDeployment(ctx context.Context) error {
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
	return nil
}

// deploymentInUse refuses privileged network teardown while any recorded
// node of the single deployment is provably (or ambiguously) live.
func deploymentInUse(ctx context.Context) error {
	root, err := state.ResolveDataRoot()
	if err != nil {
		return nil
	}
	store := state.Store{Root: root}
	entries, err := os.ReadDir(filepath.Join(root, "nodes"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	audit := privatevm.RuntimeIdentityAuditor(execx.OSRunner{Timeout: 15 * time.Second, OutputLimit: 1 << 20}, time.Second)
	for _, entry := range entries {
		node, readErr := store.ReadNode(entry.Name())
		if readErr != nil {
			continue
		}
		observation, auditErr := audit(ctx, node)
		if auditErr != nil {
			return fmt.Errorf("node %s runtime audit is inconclusive (%v); stop the deployment first", entry.Name(), auditErr)
		}
		if observation.Live {
			return fmt.Errorf("node %s is running; stop the deployment first", node.Node)
		}
	}
	return nil
}

// withoutRecordedAddresses drops addresses that belong to this deployment's
// own recorded nodes, so preflight does not report a running lab as a
// conflict.
func withoutRecordedAddresses(addresses []string) []string {
	root, err := state.ResolveDataRoot()
	if err != nil {
		return addresses
	}
	deploymentState, err := (state.Store{Root: root}).ReadDeployment()
	if err != nil {
		return addresses
	}
	recorded := make(map[string]struct{}, len(deploymentState.Resolved.Nodes))
	for _, node := range deploymentState.Resolved.Nodes {
		recorded[node.Address] = struct{}{}
	}
	filtered := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if _, mine := recorded[address]; mine {
			continue
		}
		filtered = append(filtered, address)
	}
	return filtered
}

type provisionOptions struct {
	ScriptPath  string
	Sudo        bool
	Parallelism int
	Timeout     time.Duration
}

func runProvision(parent context.Context, options provisionOptions, nodes []string, stderr io.Writer) (commandOutcome, error) {
	if options.ScriptPath == "" {
		return commandOutcome{}, newUsageError(errors.New("--script is required"))
	}
	if options.Parallelism < 1 || options.Parallelism > provision.MaxParallelism {
		return commandOutcome{}, newUsageError(fmt.Errorf("provision parallelism must be 1..%d", provision.MaxParallelism))
	}
	if options.Timeout <= 0 || options.Timeout > 24*time.Hour {
		return commandOutcome{}, newUsageError(errors.New("provision timeout must be greater than zero and no more than 24h"))
	}
	script, err := provision.LoadScript(options.ScriptPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return commandOutcome{}, newUsageError(err)
		}
		return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
	}
	operationID, err := identity.NewUUID()
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	deployment, err := privatevm.Open()
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	deploymentLock, err := privatevm.AcquireLock(ctx, deployment, false)
	if err != nil {
		return commandOutcome{}, newConflictError(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = deploymentLock.Release()
		}
	}()
	deploymentState, err := (state.Store{Root: deployment.Root}).ReadDeployment()
	if err != nil {
		return commandOutcome{}, newConflictError(fmt.Errorf("provision requires an existing running deployment: %w", err))
	}
	resolved := deploymentState.Resolved

	targets := make([]provision.Target, 0)
	selectedNames := make([]string, 0)
	var recordEvent func(context.Context, string, string, string) error
	if resolved.Network == "private" {
		manager := privatevm.Manager{FarrowVersion: version.Version, OperationID: operationID, Nodes: append([]string(nil), nodes...)}
		connections, connectionErr := manager.ConnectionsLocked(ctx, deployment, deploymentLock)
		if connectionErr != nil {
			return commandOutcome{}, newExitError(provisionConnectionExit(connectionErr), connectionErr)
		}
		for _, connection := range connections {
			if connection.Host != "127.0.0.1" {
				return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, fmt.Errorf("refuse non-loopback provision endpoint for node %s", connection.Node), "", nil)
			}
			targets = append(targets, provision.Target{Node: connection.Node, User: connection.User, Port: connection.Port, PrivateKey: connection.PrivateKey, KnownHosts: connection.KnownHosts})
			selectedNames = append(selectedNames, connection.Node)
		}
		recordEvent = manager.RecordEvent
	} else {
		return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, fmt.Errorf("unsupported deployment network %q", resolved.Network), "", nil)
	}

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return commandOutcome{}, newDetailedCommandError("capability", exitCapability, err, "", nil)
	}
	sshPath, err = filepath.Abs(sshPath)
	if err != nil {
		return commandOutcome{}, newDetailedCommandError("capability", exitCapability, err, "", nil)
	}
	startMessage := fmt.Sprintf("script_sha256=%s bytes=%d sudo=%t parallel=%d targets=%s", script.SHA256, script.Size, options.Sudo, options.Parallelism, strings.Join(selectedNames, ","))
	if err := recordEvent(ctx, "provision", "info", "starting "+startMessage); err != nil {
		return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, fmt.Errorf("refuse provision without an auditable event append: %w", err), operationID, nil)
	}
	debugf(stderr, "provision operation_id=%s targets=%s timeout=%s parallel=%d sudo=%t script_sha256=%s", operationID, strings.Join(selectedNames, ","), options.Timeout, options.Parallelism, options.Sudo, script.SHA256)
	progressItem := startProgress(ctx, stderr, fmt.Sprintf("Provisioning %d node(s)", len(targets)))
	report, err := (provision.Executor{
		Runner: provision.SSHRunner{SSHPath: sshPath}, Parallelism: options.Parallelism, OperationID: operationID,
	}).Execute(ctx, script, targets, options.Sudo)
	progressItem.Stop(err)
	if errors.Is(parent.Err(), context.Canceled) {
		return commandOutcome{}, ErrCancelled
	}
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	resultParts := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		resultParts = append(resultParts, fmt.Sprintf("%s=exit:%d,duration:%dms", result.Node, result.ExitCode, result.DurationMS))
	}
	level := "info"
	if report.Failed != 0 {
		level = "error"
	}
	auditContext, auditCancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
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
		report.AuditError += "release deployment lock: " + releaseErr.Error()
	}
	renderText := func(stdout, stderr io.Writer) error { return printProvisionReport(stdout, stderr, report) }
	outcome := commandOutcome{payload: report, text: renderText}
	if report.AuditError != "" {
		return commandOutcome{}, newRenderedCommandError("integrity", exitIntegrity, errors.New(report.AuditError), operationID, report, renderText)
	}
	if report.Failed == 0 {
		return outcome, nil
	}
	if report.Successful > 0 {
		return commandOutcome{}, newSilentRenderedCommandError("partial", exitPartial, errors.New("provision partially failed"), report, renderText)
	}
	if len(report.Results) == 1 {
		code := report.Results[0].ExitCode
		if code > 0 && code != 255 {
			return commandOutcome{}, newSilentRenderedCommandError("remote_exit", code, fmt.Errorf("remote provision exited with status %d", code), report, renderText)
		}
	}
	return commandOutcome{}, newSilentRenderedCommandError("runtime", exitRuntime, errors.New("provision failed"), report, renderText)
}

func lifecycleReadsConfig(command string) bool {
	return command == "up" || command == "plan" || command == "recreate" || command == "reload"
}

func loadLifecycleConfig(command, configPath string) (spec.Resolved, bool, error) {
	if !lifecycleReadsConfig(command) {
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

func printPrivateStatus(out io.Writer, status privatevm.Status) error {
	textField(out, 12, "spec hash", status.SpecHash)
	for _, node := range status.Nodes {
		if _, err := fmt.Fprintf(out, "%-16s %s  runtime=%s  arch=%s  accel=%s  address=%s  ssh=%s:%d", node.Name, statusValue(out, string(node.State)), node.Runtime, node.GuestArch, node.Accel, node.Address, node.SSHHost, node.SSHPort); err != nil {
			return err
		}
		if node.ProcessID > 0 {
			if _, err := fmt.Fprintf(out, " pid=%d", node.ProcessID); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	if status.Message != "" {
		if _, err := fmt.Fprintln(out, status.Message); err != nil {
			return err
		}
	}
	return nil
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

type sshConfigReconciler interface {
	InstallSSHConfig(context.Context, string, string) (sshconfig.Result, error)
	RemoveSSHConfig(string, string) (sshconfig.Result, error)
}

func lifecycleSSHConfigAction(command string, deploymentHasNodes bool) string {
	switch command {
	case "up", "reload", "recreate":
		return "install"
	case "destroy":
		if deploymentHasNodes {
			return "install"
		}
		return "remove"
	default:
		return ""
	}
}

func destroyLeavesDeploymentNodes(resolved spec.Resolved, selected []string) bool {
	return len(selected) != 0 && len(selected) < len(resolved.Nodes)
}

func reconcileLifecycleSSHConfig(ctx context.Context, command string, deploymentHasNodes bool, reconciler sshConfigReconciler) (*sshconfig.Result, error) {
	switch lifecycleSSHConfigAction(command, deploymentHasNodes) {
	case "install":
		result, err := reconciler.InstallSSHConfig(ctx, "farrow", "")
		return &result, err
	case "remove":
		result, err := reconciler.RemoveSSHConfig("farrow", "")
		return &result, err
	default:
		return nil, nil
	}
}

func fullDeploymentSSHManager(manager privatevm.Manager) privatevm.Manager {
	manager.Nodes = nil
	return manager
}

type lifecycleSSHConfigFailure struct {
	Command string
	Status  privatevm.Status
	Result  sshconfig.Result
	Err     error
}

func (failure *lifecycleSSHConfigFailure) Error() string {
	return fmt.Sprintf("%s completed its VM lifecycle step, but the SSH client configuration could not be reconciled: %v", failure.Command, failure.Err)
}

func (failure *lifecycleSSHConfigFailure) Unwrap() error { return failure.Err }

type lifecycleSSHConfigFailurePayload struct {
	Error     string           `json:"error"`
	Message   string           `json:"message"`
	Command   string           `json:"command"`
	Partial   bool             `json:"partial"`
	Status    privatevm.Status `json:"status"`
	SSHConfig sshconfig.Result `json:"ssh_config"`
}

func classifyLifecycleSSHConfigFailure(failure *lifecycleSSHConfigFailure) error {
	payload := lifecycleSSHConfigFailurePayload{
		Error: "ssh_config", Message: failure.Error(), Command: failure.Command,
		Partial: true, Status: failure.Status, SSHConfig: failure.Result,
	}
	return newRenderedCommandError("ssh_config", exitIntegrity, failure, failure.Status.OperationID, payload, func(stdout, _ io.Writer) error {
		return printPrivateStatus(stdout, failure.Status)
	})
}

func classifyPrivateLifecycleError(err error, operationID string) error {
	if errors.Is(err, privatevm.ErrRecreateRequired) {
		return newDetailedCommandError("recreate_required", exitConflict, err, operationID, nil)
	}
	if errors.Is(err, privatevm.ErrNodesRemoved) {
		return newDetailedCommandError("nodes_removed", exitConflict, err, operationID, nil)
	}
	var networkPreflight *privatevm.NetworkPreflightError
	if errors.As(err, &networkPreflight) {
		return newDetailedCommandError("network_preflight", networkPreflight.Report.ExitCode, networkPreflight, operationID, networkPreflight.Report)
	}
	var capability *privatevm.CapabilityError
	if errors.As(err, &capability) {
		return newDetailedCommandError("capability", exitCapability, capability, operationID, nil)
	}
	var partial *privatevm.PartialError
	if errors.As(err, &partial) {
		payload := lifecyclePartialFailure{Error: "partial", Message: partial.Error(), OperationID: operationID, Nodes: partial.Nodes, RolledBack: partial.RolledBack}
		return newDetailedCommandError("partial", exitPartial, partial, operationID, payload)
	}
	var deleteErr *persistentDeleteError
	if errors.As(err, &deleteErr) {
		return newDetailedCommandError("persistent_delete", exitIntegrity, deleteErr, operationID, nil)
	}
	return newDetailedCommandError("runtime", exitRuntime, err, operationID, nil)
}

type lifecycleResult struct {
	privatevm.Status
	SSHConfig *sshconfig.Result `json:"ssh_config,omitempty"`
}

func runPrivateCommand(parent context.Context, command string, resolved spec.Resolved, nodes []string, repository string, force, deletePersistent, purge, noWait, rollback bool, stderr io.Writer) (commandOutcome, error) {
	operationID := ""
	if command != "status" && command != "plan" {
		var err error
		operationID, err = identity.NewUUID()
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
	}
	var progressItem *progress
	manager := privatevm.Manager{FarrowVersion: version.Version, OperationID: operationID, Repository: repository, NoWait: noWait, RollbackFailed: rollback, Nodes: append([]string(nil), nodes...)}
	manager.Progress = deferredProgressReporter(&progressItem)
	if command == "plan" {
		ctx, cancel := context.WithTimeout(parent, time.Minute)
		defer cancel()
		plan, err := manager.Plan(ctx, resolved)
		if err != nil {
			return commandOutcome{}, classifyPrivateLifecycleError(err, operationID)
		}
		return commandOutcome{payload: plan, text: func(stdout, _ io.Writer) error {
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
			return nil
		}}, nil
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
	case "reload":
		timeout = withReadinessTimeout(20*time.Minute, resolved)
		operation = func(ctx context.Context) (privatevm.Status, error) { return manager.Reload(ctx, resolved) }
	case "recreate":
		if err := confirmCLIAction(force, "recreate", stderr); err != nil {
			if errors.Is(err, ErrCancelled) {
				return commandOutcome{}, ErrCancelled
			}
			return commandOutcome{}, newUsageError(err)
		}
		timeout = withReadinessTimeout(20*time.Minute, resolved)
		operation = func(ctx context.Context) (privatevm.Status, error) { return manager.RecreateResolved(ctx, resolved) }
	case "status":
		operation = manager.Status
	case "destroy":
		if len(nodes) != 0 && (deletePersistent || purge) {
			return commandOutcome{}, newUsageError(errors.New("--delete-persistent and --purge apply to whole-deployment destroy only"))
		}
		if !force {
			// State the exact scope before the typed confirmation.
			if _, err := fmt.Fprintln(stderr, destroyScope(resolved, nodes, deletePersistent, purge)); err != nil {
				return commandOutcome{}, newRuntimeError(err)
			}
		}
		if err := confirmCLIAction(force, "destroy", stderr); err != nil {
			if errors.Is(err, ErrCancelled) {
				return commandOutcome{}, ErrCancelled
			}
			return commandOutcome{}, newUsageError(err)
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
		return commandOutcome{}, newUsageError(fmt.Errorf("unsupported private command %q", command))
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	debugf(stderr, "lifecycle=%s mode=private timeout=%s operation_id=%s nodes=%d", command, timeout, operationID, len(resolved.Nodes))
	if command != "status" {
		progressItem = startProgress(ctx, stderr, lifecycleMessage(command))
	}
	status, err := operation(ctx)
	status.OperationID = operationID
	if errors.Is(parent.Err(), context.Canceled) {
		progressItem.Stop(ErrCancelled)
		return commandOutcome{}, ErrCancelled
	}
	lifecycleSucceeded := err == nil
	var reconciledSSHConfig *sshconfig.Result
	if err == nil {
		deploymentHasNodes := true
		if command == "destroy" {
			// A successful destroy has already validated selectors as known and
			// unique. Covering the complete pre-operation resolved set removes
			// state.json, so decide removal from that known set instead of trying
			// to read state that is intentionally gone.
			deploymentHasNodes = destroyLeavesDeploymentNodes(resolved, nodes)
		}
		if action := lifecycleSSHConfigAction(command, deploymentHasNodes); err == nil && action != "" {
			progressItem.Report(activity.Event{Phase: "ssh-config", Message: "Reconciling the marker-owned SSH client configuration"})
			var integrationErr error
			reconciler := fullDeploymentSSHManager(manager)
			reconciledSSHConfig, integrationErr = reconcileLifecycleSSHConfig(ctx, command, deploymentHasNodes, reconciler)
			if integrationErr != nil {
				err = &lifecycleSSHConfigFailure{Command: command, Status: status, Result: *reconciledSSHConfig, Err: integrationErr}
			} else {
				progressItem.Report(activity.Event{Phase: "ssh-config", Message: "SSH client configuration is synchronized", Done: true})
			}
		}
	}
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
	if lifecycleSucceeded && command == "destroy" && purge {
		if purgeErr := purgeDeployment(ctx); purgeErr != nil {
			var integrationFailure *lifecycleSSHConfigFailure
			if errors.As(err, &integrationFailure) {
				integrationFailure.Err = fmt.Errorf("%v; deployment purge also failed: %w", integrationFailure.Err, purgeErr)
			} else if err == nil {
				err = fmt.Errorf("destroy succeeded but purge failed: %w", purgeErr)
			} else {
				err = fmt.Errorf("%v; deployment purge also failed: %w", err, purgeErr)
			}
		} else {
			status.Message = strings.TrimSpace(status.Message + "; purged deployment keys and state; images remain cached")
		}
	}
	progressItem.Stop(err)
	if errors.Is(parent.Err(), context.Canceled) {
		return commandOutcome{}, ErrCancelled
	}
	if err != nil {
		var integrationFailure *lifecycleSSHConfigFailure
		if errors.As(err, &integrationFailure) {
			return commandOutcome{}, classifyLifecycleSSHConfigFailure(integrationFailure)
		}
		return commandOutcome{}, classifyPrivateLifecycleError(err, operationID)
	}
	if command == "up" || command == "reload" {
		suggestHostsPublication(resolved, stderr)
	}
	if command == "up" || command == "reload" || command == "start" || command == "restart" || command == "recreate" {
		seen := make(map[string]struct{})
		guestArch := lifecycleImageArch(resolved)
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
				if info, infoErr := service.InfoArch(ctx, alias, guestArch); infoErr == nil {
					printImageStatusWarning(stderr, info.Entry)
				}
			}
		}
	}
	result := lifecycleResult{Status: status, SSHConfig: reconciledSSHConfig}
	return commandOutcome{payload: result, text: func(stdout, _ io.Writer) error {
		if err := printPrivateStatus(stdout, status); err != nil {
			return err
		}
		if reconciledSSHConfig != nil {
			textField(stdout, 12, "ssh config", fmt.Sprintf("%s (action=%s changed=%t)", reconciledSSHConfig.Fragment, reconciledSSHConfig.Action, reconciledSSHConfig.Changed))
		}
		return nil
	}}, nil
}

func runLifecycleCommand(ctx context.Context, command string, options lifecycleOptions, nodes []string, stderr io.Writer) (commandOutcome, error) {
	if (options.DeletePersistent || options.Purge) && !options.Force && !term.IsTerminal(int(os.Stdin.Fd())) {
		// On a terminal the interactive destroy confirmation covers the
		// widened scope; without one, automation must state --force.
		return commandOutcome{}, newUsageError(errors.New("--delete-persistent and --purge require the separate --force destroy confirmation"))
	}
	if options.Purge {
		// A purge is a terminal disposal; retained persistent disks make no
		// sense past it.
		options.DeletePersistent = true
	}
	resolvedFile, hasConfig, err := loadLifecycleConfig(command, options.ConfigPath)
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	persisted, persistedErr := currentProjectResolved()
	switch {
	case persistedErr == nil && persisted.Network == "private":
		if !hasConfig {
			resolvedFile = persisted
			hasConfig = true
		}
	case persistedErr == nil && persisted.Network == "user":
		return commandOutcome{}, newConflictError(errors.New(legacyDeploymentMessage))
	}
	if !hasConfig {
		message := config.ErrNoConfig.Error()
		if !lifecycleReadsConfig(command) {
			message = "no deployment state found; run `farrow up` first"
		}
		return commandOutcome{}, newConflictError(errors.New(message))
	}
	// Selectors are checked against the same specification the engine will
	// use, before host preflight, confirmation prompts, or any change.
	if err := validateNodeSelectors(resolvedFile, nodes); err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	printWarnings(stderr, configurationWarnings(resolvedFile))
	return runPrivateCommand(ctx, command, resolvedFile, nodes, options.Repository, options.Force, options.DeletePersistent, options.Purge, options.NoWait, options.Rollback, stderr)
}

func validateNodeSelectors(resolved spec.Resolved, nodes []string) error {
	known := make(map[string]struct{}, len(resolved.Nodes))
	for _, node := range resolved.Nodes {
		known[node.Name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(nodes))
	for _, name := range nodes {
		if _, ok := known[name]; !ok {
			return fmt.Errorf("the deployment has no node %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("node %q is selected more than once", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// destroyScope states exactly what a destroy will remove so the typed
// confirmation covers a reviewed scope rather than a bare verb.
func destroyScope(resolved spec.Resolved, nodes []string, deletePersistent, purge bool) string {
	names := make([]string, 0, len(resolved.Nodes))
	for _, node := range resolved.Nodes {
		names = append(names, node.Name)
	}
	if len(nodes) != 0 {
		return fmt.Sprintf("destroy scope: node(s) %s are removed from the deployment; the other nodes stay", strings.Join(nodes, ", "))
	}
	scope := fmt.Sprintf("destroy scope: the whole deployment (%d node(s): %s)", len(names), strings.Join(names, ", "))
	switch {
	case purge:
		scope += "; also deletes persistent data disks, the deployment keys, and the deployment state (images stay cached)"
	case deletePersistent:
		scope += "; also deletes persistent data disks (keys and state are preserved)"
	default:
		scope += "; persistent data disks, keys, and state are preserved"
	}
	return scope
}

type sshConfigOptions struct {
	Install bool
	Remove  bool
	Name    string
}

func sshConfigOutcome(result sshconfig.Result, name string) commandOutcome {
	return commandOutcome{payload: result, text: func(stdout, _ io.Writer) error {
		return writeText(stdout, "%s SSH config %s (changed=%t)\nfragment: %s\nconfig: %s\n", result.Action, name, result.Changed, result.Fragment, result.Config)
	}}
}

func runSSHConfig(parent context.Context, options sshConfigOptions, nodes []string) (commandOutcome, error) {
	if options.Install && options.Remove {
		return commandOutcome{}, newUsageError(errors.New("--install and --remove are mutually exclusive"))
	}
	if options.Name == "" {
		options.Name = "farrow"
	}
	if options.Remove && len(nodes) != 0 {
		return commandOutcome{}, newUsageError(errors.New("ssh-config --remove removes the deployment fragment and does not accept node selectors"))
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	// Removal is intentionally state-independent: destroy preserves the
	// deployment state, and sshconfig.Remove itself deletes only the exact
	// marker-owned fragment and Include. This keeps rollback available
	// after resolved state or node artifacts are gone.
	if options.Remove {
		result, err := removeSSHConfigFragment(options.Name)
		if err != nil {
			return commandOutcome{}, classifySSHConfigFailure(result, err)
		}
		return sshConfigOutcome(result, options.Name), nil
	}
	resolved, resolveErr := currentProjectResolved()
	if resolveErr != nil {
		return commandOutcome{}, newConflictError(errors.New("no deployment state found; run `farrow up` first"))
	}
	if resolved.Network != "private" {
		return commandOutcome{}, newConflictError(errors.New(legacyDeploymentMessage))
	}
	manager := privatevm.Manager{FarrowVersion: version.Version, Nodes: append([]string(nil), nodes...)}
	if options.Install {
		result, err := manager.InstallSSHConfig(ctx, options.Name, "")
		if err != nil {
			return commandOutcome{}, classifySSHConfigFailure(result, err)
		}
		return sshConfigOutcome(result, options.Name), nil
	}
	text, err := manager.SSHConfig(ctx)
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	return commandOutcome{payload: map[string]string{"config": text}, text: func(stdout, _ io.Writer) error {
		_, err := fmt.Fprint(stdout, text)
		return err
	}}, nil
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

type sshConfigFailurePayload struct {
	Error   string           `json:"error"`
	Message string           `json:"message"`
	Partial bool             `json:"partial"`
	Result  sshconfig.Result `json:"result"`
}

func classifySSHConfigFailure(result sshconfig.Result, err error) error {
	partial := result.Changed && strings.HasSuffix(result.Action, "-partial")
	payload := sshConfigFailurePayload{Error: "ssh_config", Message: err.Error(), Partial: partial, Result: result}
	if partial {
		err = fmt.Errorf("SSH config operation partially changed owned state; retry is safe (action=%s fragment=%s config=%s): %w", result.Action, result.Fragment, result.Config, err)
		payload.Message = err.Error()
	}
	return newDetailedCommandError("ssh_config", exitIntegrity, err, "", payload)
}

func runHosts(parent context.Context, action string, apply bool, stderr io.Writer) (commandOutcome, error) {
	if action != hostconfig.ActionInstall && action != hostconfig.ActionUninstall {
		return commandOutcome{}, newUsageError(fmt.Errorf("unknown hosts action %q", action))
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	manager := privatevm.Manager{FarrowVersion: version.Version}
	var entries []hostconfig.Entry
	var err error
	if action == hostconfig.ActionInstall {
		entries, err = manager.HostEntries(ctx)
		if err != nil {
			return commandOutcome{}, newDetailedCommandError("capability", exitCapability, err, "", nil)
		}
	}
	baseRunner := execx.OSRunner{Timeout: 30 * time.Second, OutputLimit: 1 << 20}
	rootRunner := setupRootRunner(baseRunner)
	if apply {
		privilege := &sudoSession{base: baseRunner, stderr: stderr, scope: "hosts command"}
		defer privilege.close()
		if err := privilege.ensure(ctx, "apply the reviewed marker-owned /etc/hosts plan"); err != nil {
			return commandOutcome{}, newDetailedCommandError("capability", exitCapability, err, "", nil)
		}
	}
	debugf(stderr, "hosts action=%s entries=%d apply=%t", action, len(entries), apply)
	progressMessage := "Installing deployment host entries"
	if action == hostconfig.ActionUninstall {
		progressMessage = "Removing deployment host entries"
	}
	progressItem := startProgress(ctx, stderr, progressMessage)
	report, err := (hostconfig.Executor{Root: rootRunner}).Execute(ctx, action, entries, apply)
	progressItem.Stop(err)
	if errors.Is(parent.Err(), context.Canceled) {
		return commandOutcome{}, ErrCancelled
	}
	if err != nil {
		return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
	}
	return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
		textField(stdout, 16, "action", statusValue(stdout, report.Plan.Action))
		textField(stdout, 16, "target", report.Plan.Target)
		textField(stdout, 16, "changed", report.Plan.Changed)
		textField(stdout, 16, "applied", report.Applied)
		textField(stdout, 16, "before sha256", report.Plan.BeforeSHA256)
		textField(stdout, 16, "after sha256", report.Plan.AfterSHA256)
		textField(stdout, 16, "helper", report.Plan.HelperPath)
		textField(stdout, 16, "helper sha256", report.Plan.HelperSHA256)
		for _, line := range report.Plan.Lines {
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return err
			}
		}
		if report.Plan.Changed && !report.Applied {
			_, err := fmt.Fprintln(stdout, "rerun with --yes after reviewing this exact marker-owned plan; Farrow will request sudo when needed")
			return err
		}
		return nil
	}}, nil
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

type logOptions struct {
	Source string
	Follow bool
}

func runLogs(parent context.Context, options logOptions, requestedNode string, stdout, stderr io.Writer) (commandOutcome, error) {
	if options.Source == "" {
		options.Source = "serial"
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	resolved, resolveErr := currentProjectResolved()
	if resolveErr != nil {
		return commandOutcome{}, newConflictError(errors.New("no deployment state found; run `farrow up` first"))
	}
	if resolved.Network != "private" {
		return commandOutcome{}, newConflictError(errors.New(legacyDeploymentMessage))
	}
	node := requestedNode
	if options.Source == "events" {
		if node != "" {
			return commandOutcome{}, newUsageError(errors.New("--source events is the deployment-wide event log and does not accept a node"))
		}
		node = "deployment"
	}
	path, err := (privatevm.Manager{FarrowVersion: version.Version}).LogPath(requestedNode, options.Source)
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	if node == "" {
		for _, candidate := range resolved.Nodes {
			if candidate.Control {
				node = candidate.Name
				break
			}
		}
	}
	handle, err := os.Open(path)
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	defer func() {
		// Log reads are read-only; content has already been consumed by the time
		// this descriptor is released.
		_ = handle.Close()
	}()
	if structuredOutput(stdout) && !options.Follow {
		info, err := handle.Stat()
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		capture := boundedCapture{limit: structuredCommandCaptureLimit}
		if _, err := io.Copy(&capture, handle); err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		content := capture.String()
		result := logResult{
			Node: node, Source: options.Source, Path: path, Content: content,
			Bytes: len(content), TotalBytes: info.Size(), Truncated: capture.truncated,
		}
		return commandOutcome{payload: result}, nil
	}
	if structuredOutput(stdout) {
		if err := encodeStreamOutput(stdout, logStreamRecord{Type: "start", Node: node, Source: options.Source, Path: path}); err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		reader := bufio.NewReaderSize(handle, structuredLogRecordLimit)
		var sequence uint64
		for {
			chunk, continued, readErr := readStructuredLogChunk(reader)
			if chunk != "" {
				sequence++
				if err := encodeStreamOutput(stdout, logStreamRecord{
					Type: "line", Node: node, Source: options.Source, Path: path, Sequence: sequence,
					Content: chunk, Continued: continued,
				}); err != nil {
					return commandOutcome{}, newRuntimeError(err)
				}
			}
			if readErr == nil {
				continue
			}
			if !errors.Is(readErr, io.EOF) {
				return commandOutcome{}, newRuntimeError(readErr)
			}
			select {
			case <-ctx.Done():
				return commandOutcome{}, ErrCancelled
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if !options.Follow {
		content, err := io.ReadAll(handle)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		result := logResult{Node: node, Source: options.Source, Path: path, Content: string(content), Bytes: len(content), TotalBytes: int64(len(content))}
		return commandOutcome{payload: result, text: func(stdout, _ io.Writer) error {
			_, err := stdout.Write(content)
			return err
		}}, nil
	}
	if _, err := io.Copy(stdout, handle); err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	bestEffortf(stderr, "%s following %s log for %s\n", styled(stderr, ansiCyan, "→"), options.Source, node)
	for options.Follow {
		select {
		case <-ctx.Done():
			return commandOutcome{}, ErrCancelled
		case <-time.After(250 * time.Millisecond):
			if _, err := io.Copy(stdout, handle); err != nil {
				return commandOutcome{}, newRuntimeError(err)
			}
		}
	}
	return commandOutcome{streamed: true}, nil
}

type validateResult struct {
	Valid    bool          `json:"valid"`
	Source   string        `json:"source"`
	SpecHash string        `json:"spec_hash"`
	Resolved spec.Resolved `json:"resolved"`
	Warnings []string      `json:"warnings,omitempty"`
}

func runValidate(filePath string) (commandOutcome, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	file, source, err := config.Discover(cwd, filePath)
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	resolved, err := file.Resolve()
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	hash, err := spec.Hash(resolved)
	if err != nil {
		return commandOutcome{}, newRuntimeError(err)
	}
	warnings := configurationWarnings(resolved)
	result := validateResult{Valid: true, Source: source, SpecHash: hash, Resolved: resolved, Warnings: warnings}
	return commandOutcome{payload: result, text: func(stdout, stderr io.Writer) error {
		printWarnings(stderr, warnings)
		textField(stdout, 12, "status", statusValue(stdout, "valid"))
		textField(stdout, 12, "source", source)
		textField(stdout, 12, "spec hash", hash)
		return nil
	}}, nil
}

type initOptions struct {
	Template string
	CIDR     string
	Output   string
	Force    bool
}

type initResult struct {
	Template string `json:"template"`
	Content  string `json:"content,omitempty"`
	Path     string `json:"path,omitempty"`
}

func runInit(options initOptions) (commandOutcome, error) {
	name := options.Template
	if name == "" {
		name = "meta"
	}
	data, err := config.Template(name, options.CIDR)
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	warnings := make([]string, 0, 1)
	if options.CIDR != "" {
		if layout, parseErr := subnet.Parse(options.CIDR); parseErr == nil {
			if warning := layout.Warning(); warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}
	if options.Output == "-" {
		result := initResult{Template: name, Content: string(data)}
		return commandOutcome{payload: result, text: func(stdout, stderr io.Writer) error {
			printWarnings(stderr, warnings)
			_, err := stdout.Write(data)
			return err
		}}, nil
	}
	target := options.Output
	if target == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return commandOutcome{}, newRuntimeError(cwdErr)
		}
		target = filepath.Join(cwd, "farrow.yml")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return commandOutcome{}, newUsageError(err)
	}
	if options.Force {
		err = fsutil.AtomicWrite(target, data, 0o600)
	} else {
		err = fsutil.AtomicCreate(target, data, 0o600)
	}
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return commandOutcome{}, newConflictError(fmt.Errorf("%s already exists; edit it, pass --force to replace it, or -o for another path", target))
		}
		return commandOutcome{}, newRuntimeError(err)
	}
	result := initResult{Template: name, Path: target}
	return commandOutcome{payload: result, text: func(stdout, stderr io.Writer) error {
		printWarnings(stderr, warnings)
		textField(stdout, 10, "template", name)
		textField(stdout, 10, "wrote", target)
		textField(stdout, 10, "next", "farrow setup && farrow up")
		return nil
	}}, nil
}

type imageOptions struct {
	Action         string
	Repository     string
	Alias          string
	Arch           string
	Source         string
	Path           string
	ExpectedSHA256 string
	Name           string
	Boot           string
	SourceUser     string
	DryRun         bool
	Apply          bool
	AllowDowngrade bool
}

func runImage(parent context.Context, options imageOptions, stderr io.Writer) (commandOutcome, error) {
	switch options.Action {
	case "list":
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		service, err := imageService(options.Repository, nil)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		entries, manifestState, err := service.List(ctx)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		payload := struct {
			Manifest image.ManifestState `json:"manifest"`
			Images   []image.Entry       `json:"images"`
		}{manifestState, entries}
		return commandOutcome{payload: payload, text: func(stdout, _ io.Writer) error {
			textField(stdout, 12, "manifest", manifestState.Active)
			textField(stdout, 12, "version", manifestState.ActiveVersion)
			textField(stdout, 12, "highest", manifestState.HighestVersion)
			for _, entry := range entries {
				channels := "-"
				if len(entry.Channels) != 0 {
					channels = strings.Join(entry.Channels, ",")
				}
				if err := writeText(stdout, "%s %s %s channels=%s status=%s sha256:%s\n", entry.Alias, entry.Release, entry.Arch, channels, entry.Status, entry.SHA256); err != nil {
					return err
				}
			}
			return nil
		}}, nil
	case "info", "pull":
		ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
		defer cancel()
		var progressItem *progress
		service, err := imageService(options.Repository, deferredProgressReporter(&progressItem))
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		var info image.Info
		if options.Action == "pull" {
			debugf(stderr, "image pull alias=%s timeout=%s", options.Alias, 30*time.Minute)
			progressItem = startProgress(ctx, stderr, fmt.Sprintf("Pulling image %s", options.Alias))
			if options.Arch == "" {
				info, err = service.PullAlias(ctx, options.Alias)
			} else {
				info, err = service.PullArch(ctx, options.Alias, options.Arch)
			}
		} else {
			if options.Arch == "" {
				info, err = service.Info(ctx, options.Alias)
			} else {
				info, err = service.InfoArch(ctx, options.Alias, options.Arch)
			}
		}
		progressItem.Stop(err)
		if errors.Is(parent.Err(), context.Canceled) {
			return commandOutcome{}, ErrCancelled
		}
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		printImageStatusWarning(stderr, info.Entry)
		return commandOutcome{payload: info, text: func(stdout, _ io.Writer) error {
			if err := writeText(stdout, "%s %s %s status=%s sha256:%s cached=%t\n", info.Entry.Alias, info.Entry.Release, info.Entry.Arch, info.Entry.Status, info.Entry.SHA256, info.Cached); err != nil {
				return err
			}
			if info.Path != "" {
				return writeText(stdout, "path: %s\n", info.Path)
			}
			return nil
		}}, nil
	case "repo-scan", "repo-build", "repo-verify":
		root, err := filepath.Abs(options.Path)
		if err != nil {
			return commandOutcome{}, newUsageError(err)
		}
		if options.Action == "repo-scan" {
			report, scanErr := image.ScanRepository(root)
			if scanErr != nil {
				return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, scanErr, "", nil)
			}
			return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
				textField(stdout, 12, "root", report.Root)
				textField(stdout, 12, "tracked", len(report.Tracked))
				textField(stdout, 12, "missing", len(report.Missing))
				textField(stdout, 12, "untracked", len(report.Untracked))
				textField(stdout, 12, "unsafe", len(report.Unsafe))
				return nil
			}}, nil
		}
		qemuImg, err := exec.LookPath("qemu-img")
		if err != nil {
			return commandOutcome{}, newDetailedCommandError("capability", exitCapability, fmt.Errorf("repository %s requires qemu-img: %w", strings.TrimPrefix(options.Action, "repo-"), err), "", nil)
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
		defer cancel()
		builder := image.RepoBuilder{QEMUImg: qemuImg, Runner: execx.OSRunner{Timeout: 10 * time.Minute, OutputLimit: 1 << 20}}
		var result image.RepoBuildResult
		if options.Action == "repo-build" {
			result, err = builder.Build(ctx, root)
		} else {
			result, err = builder.Verify(ctx, root)
		}
		if err != nil {
			return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
		}
		return commandOutcome{payload: result, text: func(stdout, _ io.Writer) error {
			textField(stdout, 12, "catalog", result.Path)
			textField(stdout, 12, "revision", result.Catalog.Version)
			textField(stdout, 12, "images", len(result.Catalog.Images))
			textField(stdout, 12, "bytes", result.Bytes)
			return nil
		}}, nil
	case "prune":
		if options.DryRun && options.Apply {
			return commandOutcome{}, newUsageError(errors.New("--dry-run and --yes are mutually exclusive"))
		}
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		service, err := imageService(options.Repository, nil)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		progressItem := startProgress(ctx, stderr, "Scanning unreferenced image cache entries")
		report, err := service.PruneAll(ctx, options.Apply, func(refCtx context.Context) (map[string]struct{}, error) {
			return nodeImageReferences(service.DataRoot)
		})
		progressItem.Stop(err)
		if errors.Is(parent.Err(), context.Canceled) {
			return commandOutcome{}, ErrCancelled
		}
		if err != nil {
			return commandOutcome{}, newDetailedCommandError("integrity", exitIntegrity, err, "", nil)
		}
		return commandOutcome{payload: report, text: func(stdout, _ io.Writer) error {
			if len(report.Items) == 0 {
				return writeText(stdout, "no unreferenced cache images\n")
			}
			for _, item := range report.Items {
				action := "would delete"
				if item.Applied {
					action = "deleted"
				}
				var err error
				if item.Digest == "" {
					err = writeText(stdout, "%s %s (%d bytes, %s)\n", action, item.ImagePath, item.Bytes, item.Kind)
				} else {
					err = writeText(stdout, "%s %s sha256:%s (%d bytes)\n", action, item.ImagePath, item.Digest, item.Bytes)
				}
				if err != nil {
					return err
				}
			}
			return nil
		}}, nil
	case "sync":
		if options.Source == "" {
			return commandOutcome{}, newUsageError(errors.New("image sync requires a URL or path"))
		}
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		debugf(stderr, "image manifest sync source=%s allow_downgrade=%t", progressSource(options.Source), options.AllowDowngrade)
		service, err := imageService(options.Repository, nil)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		progressItem := startProgress(ctx, stderr, "Synchronizing the image manifest")
		state, err := service.SyncManifest(ctx, options.Source, options.AllowDowngrade)
		progressItem.Stop(err)
		if errors.Is(parent.Err(), context.Canceled) {
			return commandOutcome{}, ErrCancelled
		}
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		return commandOutcome{payload: state, text: func(stdout, _ io.Writer) error {
			return writeText(stdout, "activated manifest version %d digest %s key %s\n", state.ActiveVersion, state.ActiveDigest, state.KeyID)
		}}, nil
	case "reset-manifest":
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		service, err := imageService(options.Repository, nil)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		state, err := service.ResetManifest(ctx)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		return commandOutcome{payload: state, text: func(stdout, _ io.Writer) error {
			return writeText(stdout, "active manifest reset to embedded version %d; high-water mark %d preserved\n", state.ActiveVersion, state.HighestVersion)
		}}, nil
	case "import":
		if options.Path == "" {
			return commandOutcome{}, newUsageError(errors.New("image import requires a path"))
		}
		invalidAliasMetadata := (options.Name == "" && (options.Boot != "" || options.SourceUser != "")) || (options.Name != "" && (options.SourceUser == "" || (options.Boot != "bios" && options.Boot != "uefi")))
		if invalidAliasMetadata {
			return commandOutcome{}, newUsageError(errors.New("--name requires --boot bios|uefi and --source-user; alias metadata is immutable"))
		}
		ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
		defer cancel()
		service, err := imageService("", nil)
		if err != nil {
			return commandOutcome{}, newRuntimeError(err)
		}
		var path string
		var metadata image.Metadata
		var localEntry *image.Entry
		debugf(stderr, "image import source=%s expected_sha256=%s alias=%s", options.Path, options.ExpectedSHA256, options.Name)
		progressItem := startProgress(ctx, stderr, "Importing and verifying the image")
		if options.Name == "" {
			path, metadata, err = service.ImportFile(ctx, options.Path, options.ExpectedSHA256)
		} else {
			entry, importedPath, importedMetadata, importErr := service.ImportNamed(ctx, options.Path, options.ExpectedSHA256, options.Name, options.Boot, options.SourceUser)
			path, metadata, err = importedPath, importedMetadata, importErr
			if importErr == nil {
				localEntry = &entry
			}
		}
		progressItem.Stop(err)
		if errors.Is(parent.Err(), context.Canceled) {
			return commandOutcome{}, ErrCancelled
		}
		result := struct {
			Path     string         `json:"path"`
			Metadata image.Metadata `json:"metadata"`
			Entry    *image.Entry   `json:"entry,omitempty"`
			Error    string         `json:"error,omitempty"`
			Message  string         `json:"message,omitempty"`
		}{Path: path, Metadata: metadata, Entry: localEntry}
		if err != nil {
			result.Error = "runtime"
			result.Message = err.Error()
		}
		renderText := func(stdout, _ io.Writer) error {
			if path != "" {
				return writeText(stdout, "imported %s\nsha256 %s\n", path, metadata.Digest)
			}
			return nil
		}
		if err != nil {
			return commandOutcome{}, newRenderedCommandError("runtime", exitRuntime, err, "", result, renderText)
		}
		return commandOutcome{payload: result, text: renderText}, nil
	default:
		return commandOutcome{}, newUsageError(fmt.Errorf("unknown image command %q", options.Action))
	}
}

// doctorCheckLabel is the word a person reads, chosen to match the consequence
// rather than the raw severity. A network finding is real, but it never changes
// the exit status, and printing "error" next to a successful exit is the kind of
// contradiction that teaches people to stop reading the output.
func doctorCheckLabel(check doctor.Check) string {
	if check.Class == doctor.ClassNetwork && check.Status == doctor.Error {
		return "blocked"
	}
	return string(check.Status)
}

func runDoctor(parent context.Context, stderr io.Writer) (commandOutcome, error) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	progressItem := startProgress(ctx, stderr, "Checking host capabilities")
	report := (doctor.Probe{}).Run(ctx)
	progressItem.Stop(nil)
	if errors.Is(parent.Err(), context.Canceled) {
		return commandOutcome{}, ErrCancelled
	}
	renderText := func(stdout, _ io.Writer) error {
		textField(stdout, 10, "host", fmt.Sprintf("%s/%s", report.OS, report.Arch))
		textField(stdout, 10, "tier", report.Tier)
		for _, check := range report.Checks {
			if err := writeText(stdout, "%s %-20s %s\n", statusCell(stdout, 10, doctorCheckLabel(check)), check.Name, check.Evidence); err != nil {
				return err
			}
			if check.Fix != "" {
				if err := writeText(stdout, "  fix: %s\n", check.Fix); err != nil {
					return err
				}
			}
		}
		if report.HasErrors() {
			if err := writeText(stdout, "this host cannot run Farrow guests until the errors above are resolved\n"); err != nil {
				return err
			}
		} else {
			if err := writeText(stdout, "host compute capability is ready\n"); err != nil {
				return err
			}
		}
		if !report.NetworkReady() {
			if err := writeText(stdout, "the host-global network is not ready; run `farrow setup` to prepare it\n"); err != nil {
				return err
			}
		}
		return nil
	}
	if report.HasErrors() {
		return commandOutcome{}, newSilentRenderedCommandError("capability", exitCapability, errors.New("host compute capability is not ready"), report, renderText)
	}
	return commandOutcome{payload: report, text: renderText}, nil
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	prepared, preparedStdout, preparedStderr, err := prepareOutput(args, stdout, stderr)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	if len(prepared) > 0 {
		debugf(preparedStderr, "command=%s format=%s stdout_tty=%t stderr_tty=%t", prepared[0], outputFormatFor(preparedStdout), writerTTY(preparedStdout), writerTTY(preparedStderr))
	}
	return executeCLI(ctx, prepared, preparedStdout, preparedStderr)
}

func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.TODO(), args, stdout, stderr)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

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
	warningf(stderr, "declared host aliases are not published; run `farrow hosts install --yes` to add them to %s", target)
}

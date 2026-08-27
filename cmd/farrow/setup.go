package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	darwinnet "github.com/pgsty/farrow/internal/network/darwin"
	linuxnet "github.com/pgsty/farrow/internal/network/linux"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/network/subnet"
	setuphost "github.com/pgsty/farrow/internal/setup"
	"github.com/pgsty/farrow/internal/spec"
	"golang.org/x/term"
)

type setupStep struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Changed bool   `json:"changed,omitempty"`
}

type setupResult struct {
	Schema            int                      `json:"schema"`
	OS                string                   `json:"os"`
	Arch              string                   `json:"arch"`
	Profile           string                   `json:"profile"`
	Config            string                   `json:"config,omitempty"`
	DryRun            bool                     `json:"dry_run"`
	Applicable        bool                     `json:"applicable"`
	Ready             bool                     `json:"ready"`
	Blocked           bool                     `json:"blocked"`
	Changed           bool                     `json:"changed"`
	MutationUncertain bool                     `json:"mutation_uncertain,omitempty"`
	ExitCode          int                      `json:"exit_code"`
	Error             string                   `json:"error,omitempty"`
	Dependencies      setuphost.DependencyPlan `json:"dependencies"`
	Network           *netpreflight.Report     `json:"network,omitempty"`
	NetworkCIDR       string                   `json:"network_cidr,omitempty"`
	NetworkMode       string                   `json:"network_mode,omitempty"`
	Checks            []doctor.Check           `json:"checks,omitempty"`
	Steps             []setupStep              `json:"steps"`
	Next              string                   `json:"next"`
	NextArgv          []string                 `json:"next_argv"`
	Resolution        string                   `json:"resolution,omitempty"`
}

type setupSelection struct {
	Mode            string
	Profile         string
	Resolved        spec.Resolved
	File            config.File
	ConfigData      []byte
	ConfigPath      string
	Generated       bool
	Publish         bool
	ExplicitNetwork bool
}

type setupCLIOptions struct {
	FilePath     string
	NetworkCIDR  string
	Mode         string
	Repo         string
	ModeExplicit bool
	DryRun       bool
	Yes          bool
}

func (options setupCLIOptions) arguments(profileName string) []string {
	arguments := make([]string, 0, 6)
	if profileName != "" {
		arguments = append(arguments, profileName)
	}
	if options.FilePath != "" {
		arguments = append(arguments, "--file="+options.FilePath)
	}
	if options.NetworkCIDR != "" {
		arguments = append(arguments, "--network-cidr="+options.NetworkCIDR)
	}
	if options.Repo != "" {
		arguments = append(arguments, "--repo="+options.Repo)
	}
	if options.ModeExplicit {
		arguments = append(arguments, "--mode="+options.Mode)
	}
	if options.DryRun {
		arguments = append(arguments, "--dry-run")
	}
	if options.Yes {
		arguments = append(arguments, "--yes")
	}
	return arguments
}

func loadSetupFile(path string) (config.File, spec.Resolved, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return config.File{}, spec.Resolved{}, err
	}
	file, err := config.LoadPath(absolute)
	if err != nil {
		return config.File{}, spec.Resolved{}, err
	}
	resolved, err := file.Resolve()
	return file, resolved, err
}

func generatedSetupProfile(name, cidr, target string) (setupSelection, error) {
	data, err := config.Template(name, cidr)
	if err != nil {
		return setupSelection{}, err
	}
	file, err := config.ParseInventory(data)
	if err != nil {
		return setupSelection{}, err
	}
	resolved, err := file.Resolve()
	if err != nil {
		return setupSelection{}, err
	}
	return setupSelection{
		Mode: "private", Profile: name, Resolved: resolved, File: file,
		ConfigData: data, ConfigPath: target, Generated: true, Publish: true,
		ExplicitNetwork: cidr != "",
	}, nil
}

func resolvedHash(value spec.Resolved) (string, error) { return spec.Hash(value) }

func reconcileGeneratedTarget(selection setupSelection) (setupSelection, error) {
	info, err := os.Lstat(selection.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return selection, nil
	}
	if err != nil {
		return selection, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return selection, fmt.Errorf("refuse setup config target that is not a regular file: %s", selection.ConfigPath)
	}
	existingFile, existingResolved, err := loadSetupFile(selection.ConfigPath)
	if err != nil {
		return selection, fmt.Errorf("existing configuration is invalid and was preserved: %w", err)
	}
	wantedHash, err := resolvedHash(selection.Resolved)
	if err != nil {
		return selection, err
	}
	existingHash, err := resolvedHash(existingResolved)
	if err != nil {
		return selection, err
	}
	if wantedHash != existingHash {
		return selection, fmt.Errorf("existing %s differs from profile %s; use `farrow setup -f %s`, choose an empty directory, or move the existing file", selection.ConfigPath, selection.Profile, selection.ConfigPath)
	}
	selection.File = existingFile
	selection.Resolved = existingResolved
	selection.Publish = false
	// Once the file exists it is user-owned input. Setup may reuse it but must
	// never auto-rebase it while solving a host-network conflict.
	selection.Generated = false
	return selection, nil
}

// discoverSetupConfig returns the first existing discovery-name path in cwd.
func discoverSetupConfig(cwd string) (string, error) {
	for _, name := range config.DiscoveryNames {
		candidate := filepath.Join(cwd, name)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("configuration is not a regular file: %s", candidate)
		}
		return candidate, nil
	}
	return "", nil
}

func resolveSetupSelection(profileName, filePath, networkCIDR, cwd string) (setupSelection, error) {
	target := filepath.Join(cwd, "farrow.yml")
	if filePath != "" && profileName != "" {
		return setupSelection{}, errors.New("setup accepts either a lab template or -f, not both")
	}
	if filePath != "" && networkCIDR != "" {
		return setupSelection{}, errors.New("--network-cidr cannot silently rebase a user configuration; edit the file as one coordinated change")
	}
	if filePath != "" {
		absolute, err := filepath.Abs(filePath)
		if err != nil {
			return setupSelection{}, err
		}
		file, resolved, err := loadSetupFile(absolute)
		if err != nil {
			return setupSelection{}, err
		}
		return setupSelection{Mode: "private", Resolved: resolved, File: file, ConfigPath: absolute}, nil
	}
	discovered, err := discoverSetupConfig(cwd)
	if err != nil {
		return setupSelection{}, err
	}
	if discovered != "" && profileName == "" {
		if networkCIDR != "" {
			return setupSelection{}, errors.New("--network-cidr cannot silently rebase the discovered configuration; edit the file as one coordinated change")
		}
		file, resolved, err := loadSetupFile(discovered)
		if err != nil {
			return setupSelection{}, err
		}
		return setupSelection{Mode: "private", Resolved: resolved, File: file, ConfigPath: discovered}, nil
	}
	if profileName == "" {
		profileName = "meta"
	}
	if !config.ValidTemplate(profileName) {
		return setupSelection{}, fmt.Errorf("unknown lab template %q; available: %s", profileName, strings.Join(config.TemplateNames(), ", "))
	}
	if discovered != "" {
		// Repeating `farrow setup <template>` over the file it generated is
		// idempotent; a different existing configuration is preserved.
		target = discovered
	}
	selection, err := generatedSetupProfile(profileName, networkCIDR, target)
	if err != nil {
		return setupSelection{}, err
	}
	return reconcileGeneratedTarget(selection)
}

func (selection *setupSelection) rebaseGenerated(cidr string) error {
	if !selection.Generated || selection.ExplicitNetwork {
		return errors.New("setup selection cannot be automatically rebased")
	}
	rebased, err := generatedSetupProfile(selection.Profile, cidr, selection.ConfigPath)
	if err != nil {
		return err
	}
	rebased.ExplicitNetwork = false
	resolved, err := reconcileGeneratedTarget(rebased)
	if err != nil {
		return err
	}
	*selection = resolved
	return nil
}

func setupAddresses(selection setupSelection) []string {
	addresses := make([]string, 0, len(selection.Resolved.Nodes))
	for _, node := range selection.Resolved.Nodes {
		if node.Address != "" {
			addresses = append(addresses, node.Address)
		}
	}
	return withoutRecordedAddresses(addresses)
}

func inspectSetupNetwork(ctx context.Context, selection setupSelection, runner execx.Runner) (netpreflight.Report, error) {
	if selection.Resolved.Private == nil {
		return netpreflight.Report{}, errors.New("private setup has no private network configuration")
	}
	layout, err := subnet.Parse(selection.Resolved.Private.CIDR)
	if err != nil {
		return netpreflight.Report{}, err
	}
	return netpreflight.Run(ctx, netpreflight.Request{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Purpose: netpreflight.Install,
		Layout: layout, Addresses: setupAddresses(selection),
	}, netpreflight.Probe{Runner: runner}), nil
}

func reusableInstalledNetwork(report netpreflight.Report) string {
	status := report.Installation.Status
	if (status == "exact" || status == "protected") && report.Installation.CIDR != "" && report.Installation.Healthy {
		return report.Installation.CIDR
	}
	return ""
}

var setupNetworkCandidates = []string{
	"172.31.251.0/24",
	"172.31.252.0/24",
	"192.168.250.0/24",
	"192.168.251.0/24",
}

func selectSetupNetwork(ctx context.Context, selection *setupSelection, runner execx.Runner) (netpreflight.Report, error) {
	report, err := inspectSetupNetwork(ctx, *selection, runner)
	if err != nil {
		return report, err
	}
	if report.Ready {
		return report, nil
	}
	if !selection.Generated || selection.ExplicitNetwork {
		return report, nil
	}
	if installed := reusableInstalledNetwork(report); installed != "" {
		if err := selection.rebaseGenerated(installed); err != nil {
			return report, err
		}
		return inspectSetupNetwork(ctx, *selection, runner)
	}
	if report.Installation.Status != "" && report.Installation.Status != "absent" {
		return report, nil
	}
	for _, candidate := range setupNetworkCandidates {
		if candidate == report.CIDR {
			continue
		}
		original := *selection
		if err := selection.rebaseGenerated(candidate); err != nil {
			*selection = original
			continue
		}
		candidateReport, candidateErr := inspectSetupNetwork(ctx, *selection, runner)
		if candidateErr == nil && candidateReport.Ready {
			return candidateReport, nil
		}
		*selection = original
	}
	return report, nil
}

func constrainSetupNetworkMode(report netpreflight.Report, requested string, explicit bool) (netpreflight.Report, string) {
	if report.OS != "darwin" {
		return report, "bridge"
	}
	effective := requested
	installed := report.Installation.Status == "exact" || report.Installation.Status == "protected"
	if !installed || !report.Installation.Healthy || report.Installation.Mode == "" {
		return report, effective
	}
	if !explicit {
		return report, report.Installation.Mode
	}
	if report.Installation.Mode == requested {
		return report, requested
	}
	report.Findings = append(report.Findings, netpreflight.Finding{
		Code: "installation.mode_mismatch", Severity: netpreflight.Error, Class: netpreflight.State,
		Evidence: fmt.Sprintf("installed=%s requested=%s", report.Installation.Mode, requested),
		Fix:      fmt.Sprintf("rerun setup without --mode to reuse %s, or stop private projects, uninstall the owned network, then rerun with --mode %s", report.Installation.Mode, requested),
	})
	report.Ready = false
	if report.ExitCode < exitConflict {
		report.ExitCode = exitConflict
	}
	return report, effective
}

func setupFindingError(report netpreflight.Report) error {
	lines := make([]string, 0, len(report.Findings)+1)
	for _, finding := range report.Findings {
		if finding.Severity != netpreflight.Error {
			continue
		}
		line := finding.Code + ": " + finding.Evidence
		switch finding.Code {
		case "installation.network_mismatch":
			line += "; either rebase the configuration to the installed network, or stop/destroy the deployment, run `farrow network uninstall --yes`, then rerun setup"
		case "installation.integrity":
			line += "; run `farrow network status --verbose` and repair only manifest-owned state; setup will not adopt or delete unknown paths"
		case "installation.mode_mismatch":
			line += "; " + finding.Fix
		default:
			if finding.Fix != "" {
				line += "; " + finding.Fix
			}
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "private network is not ready")
	}
	return errors.New(strings.Join(lines, "\n"))
}

func confirmSetup(yes bool, mutating bool, stdin io.Reader, stderr io.Writer) error {
	if yes || !mutating {
		return nil
	}
	terminal, ok := stdin.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(terminal.Fd())) {
		return errors.New("setup needs --yes when stdin is not a terminal")
	}
	fmt.Fprint(stderr, "Continue with this setup? [Y/n] ")
	line, err := bufio.NewReader(io.LimitReader(stdin, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return nil
	default:
		return errors.New("setup cancelled")
	}
}

// setupSudoSession asks for the user's password at most once per setup run.
// The first privileged step announces its reason and prompts; a background
// keeper then refreshes the cached credential every minute until setup
// finishes, so no later step prompts again — even when a slow download or
// package installation outlives sudo's timestamp window.
type setupSudoSession struct {
	base     execx.Runner
	stderr   io.Writer
	acquired bool
	stop     context.CancelFunc
}

func (session *setupSudoSession) ensure(ctx context.Context, reason string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if session.acquired {
		return nil
	}
	if reason != "" {
		fmt.Fprintf(session.stderr, "%s sudo needed: %s\n", styled(session.stderr, ansiCyan, "→"), reason)
		fmt.Fprintln(session.stderr, "  one password prompt covers this whole setup run")
	}
	const sudo = "/usr/bin/sudo"
	if term.IsTerminal(int(os.Stdin.Fd())) {
		command := exec.CommandContext(ctx, sudo, "-v")
		command.Stdin = os.Stdin
		command.Stdout = rawWriter(session.stderr)
		command.Stderr = rawWriter(session.stderr)
		if err := command.Run(); err != nil {
			return fmt.Errorf("acquire sudo credential: %w", err)
		}
	} else if _, err := session.base.Run(ctx, sudo, "-n", "-v"); err != nil {
		return errors.New("setup requires an existing non-interactive sudo credential; run sudo -v or use a terminal")
	}
	keeperContext, cancel := context.WithCancel(ctx)
	session.stop = cancel
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-keeperContext.Done():
				return
			case <-ticker.C:
				_, _ = session.base.Run(keeperContext, sudo, "-n", "-v")
			}
		}
	}()
	session.acquired = true
	return nil
}

func (session *setupSudoSession) close() {
	if session.stop != nil {
		session.stop()
	}
}

func setupRootRunner(base execx.Runner) execx.Runner {
	if os.Geteuid() == 0 {
		return base
	}
	return sudoRunner{base: base}
}

func runSetupCommands(ctx context.Context, commands []setuphost.Command, base execx.Runner, sudo *setupSudoSession, stderr io.Writer) (changed, mutationUncertain bool, err error) {
	for _, command := range commands {
		if err := command.Validate(); err != nil {
			return changed, false, err
		}
		if command.Root {
			if err := sudo.ensure(ctx, command.Name+" (system package manager)"); err != nil {
				return changed, false, err
			}
		}
		runner := base
		if command.Root {
			runner = setupRootRunner(base)
		}
		progressItem := startProgress(ctx, stderr, command.Name)
		result, err := runner.Run(ctx, command.Binary, command.Args...)
		progressItem.Stop(err)
		if err != nil {
			debugf(stderr, "setup command failed name=%s exit=%d stderr=%q", command.Name, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
			return changed, true, fmt.Errorf("%s failed with exit code %d", command.Name, result.ExitCode)
		}
		changed = true
		debugf(stderr, "setup command complete name=%s duration=%s stdout=%q stderr=%q", command.Name, result.Duration.Round(time.Millisecond), strings.TrimSpace(string(result.Stdout)), strings.TrimSpace(string(result.Stderr)))
	}
	return changed, false, nil
}

func setupNeedsNetworkInstall(report netpreflight.Report) bool {
	return report.Installation.Status == "" || report.Installation.Status == "absent"
}

func applySetupNetwork(ctx context.Context, selection setupSelection, mode, repo string, report netpreflight.Report, base execx.Runner, sudo *setupSudoSession, stderr io.Writer) (setupStep, bool, error) {
	if !setupNeedsNetworkInstall(report) {
		tickf(stderr, "Network %s is already installed", report.CIDR)
		return setupStep{Name: "network", Status: "ready", Detail: report.CIDR}, false, nil
	}
	// Refresh immediately before the network transaction. Package installation
	// can legitimately outlive sudo's timestamp window.
	networkReason := "install the host-global " + report.CIDR + " network (root-owned socket_vmnet service)"
	if runtime.GOOS != "darwin" {
		networkReason = "install the host-global " + report.CIDR + " network (root-owned farrow0 bridge)"
	}
	if err := sudo.ensure(ctx, networkReason); err != nil {
		return setupStep{}, false, err
	}
	if runtime.GOOS == "darwin" {
		interfaceID, err := identity.NewUUID()
		if err != nil {
			return setupStep{}, false, err
		}
		executor := darwinnet.Executor{User: base, Root: setupRootRunner(base)}
		sources := setuphost.SourcesFromEnvironment(repo)
		// Prefer the version-matched Homebrew formula when no pinned archive
		// is already local: brew honors the user's mirror and proxy
		// configuration, so nothing has to reach github.com.
		if sources.Archive == "" && !setuphost.SocketVMNetCached(runtime.GOARCH) {
			if binaries, ok := setupHomebrewSocketVMNet(ctx, base, stderr); ok {
				progressItem := startProgress(ctx, stderr, "Installing the private network (socket_vmnet from Homebrew, root-owned copy)")
				installReport, err := executor.InstallFromHomebrew(ctx, binaries, interfaceID, runtime.GOARCH, mode, report.CIDR, true)
				progressItem.Stop(err)
				if err != nil {
					return setupStep{}, true, err
				}
				return setupStep{Name: "network", Status: "installed", Detail: installReport.Plan.State.CIDR, Changed: installReport.Applied}, false, nil
			}
		}
		var downloadProgress *progress
		sources.Progress = deferredProgressReporter(&downloadProgress)
		downloadProgress = startProgress(ctx, stderr, "Fetching socket_vmnet "+darwinnet.ReleaseVersion+" (digest-pinned; sources: cache, FARROW_VMNET_ARCHIVE, FARROW_REPO, github.com)")
		download, err := setuphost.DownloadPinnedSocketVMNet(ctx, runtime.GOARCH, "", nil, sources)
		downloadProgress.Stop(err)
		if err != nil {
			return setupStep{}, false, err
		}
		debugf(stderr, "socket_vmnet source=%s downloaded=%t", download.URL, download.Downloaded)
		progressItem := startProgress(ctx, stderr, "Installing the private network")
		installReport, err := executor.InstallModeNetwork(ctx, download.Path, interfaceID, runtime.GOARCH, mode, report.CIDR, true)
		progressItem.Stop(err)
		if err != nil {
			return setupStep{}, true, err
		}
		return setupStep{Name: "network", Status: "installed", Detail: installReport.Plan.State.CIDR, Changed: installReport.Applied}, false, nil
	}
	linuxConfig, err := linuxnet.ConfigForCIDR(report.CIDR)
	if err != nil {
		return setupStep{}, false, err
	}
	executor := linuxnet.Executor{User: base, Root: setupRootRunner(base)}
	progressItem := startProgress(ctx, stderr, "Installing the private network")
	installReport, err := executor.InstallConfig(ctx, linuxConfig, true)
	progressItem.Stop(err)
	if err != nil {
		return setupStep{}, true, err
	}
	return setupStep{Name: "network", Status: "installed", Detail: report.CIDR, Changed: installReport.Applied}, false, nil
}

// setupHomebrewSocketVMNet resolves version-matched socket_vmnet binaries via
// Homebrew, installing the formula (always as the user, never root) when it
// is absent. false means the Homebrew source does not apply and the caller
// falls back to the digest-pinned release download.
func setupHomebrewSocketVMNet(ctx context.Context, base execx.Runner, stderr io.Writer) (darwinnet.LocalBinaries, bool) {
	probe := darwinnet.HomebrewProbe{Runner: base}
	discovery, err := probe.Discover(ctx)
	if err != nil {
		debugf(stderr, "homebrew socket_vmnet discovery failed: %v", err)
		return darwinnet.LocalBinaries{}, false
	}
	if discovery.Status == darwinnet.HomebrewFormulaMissing {
		brew, ok := probe.Brew()
		if !ok {
			return darwinnet.LocalBinaries{}, false
		}
		progressItem := startProgress(ctx, stderr, "Installing socket_vmnet "+darwinnet.ReleaseVersion+" (brew install "+darwinnet.SocketVMNetFormula+")")
		_, installErr := base.Run(ctx, brew, "install", darwinnet.SocketVMNetFormula)
		progressItem.Stop(installErr)
		if installErr != nil {
			fmt.Fprintf(stderr, "%s brew install %s failed; falling back to the digest-pinned release download\n", styled(stderr, ansiYellow, "!"), darwinnet.SocketVMNetFormula)
			return darwinnet.LocalBinaries{}, false
		}
		discovery, err = probe.Discover(ctx)
		if err != nil {
			debugf(stderr, "homebrew socket_vmnet discovery failed: %v", err)
			return darwinnet.LocalBinaries{}, false
		}
	}
	if discovery.Status != darwinnet.HomebrewFound {
		if discovery.Reason != "" {
			debugf(stderr, "homebrew socket_vmnet unavailable: %s", discovery.Reason)
		}
		return darwinnet.LocalBinaries{}, false
	}
	return discovery.Binaries, true
}

func companionHostsHelper() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(executable); canonicalErr == nil {
		executable = canonical
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(executable)
	candidates := []string{
		filepath.Join(directory, "farrow-hosts-helper"),
		filepath.Join(filepath.Dir(directory), "libexec", "farrow-hosts-helper"),
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, err := hostconfig.CompanionHelperDigest(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("matching farrow-hosts-helper was not found beside the CLI or in its package libexec directory")
}

func ensureSetupHostsHelper(ctx context.Context, base execx.Runner, sudo *setupSudoSession, stderr io.Writer) (setupStep, bool, error) {
	if digest, err := hostconfig.InstalledHelperDigest(); err == nil {
		tickf(stderr, "Hosts helper is already installed")
		return setupStep{Name: "hosts-helper", Status: "ready", Detail: digest}, false, nil
	}
	targetExists := false
	if _, err := os.Lstat(hostconfig.InstalledHelperPath); err == nil {
		if _, safeErr := hostconfig.RootOwnedHelperDigest(hostconfig.InstalledHelperPath); safeErr != nil {
			return setupStep{}, false, fmt.Errorf("existing privileged hosts helper is unsafe and was preserved: %w", safeErr)
		}
		targetExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return setupStep{}, false, err
	}
	source, err := companionHostsHelper()
	if err != nil {
		return setupStep{}, false, err
	}
	digest, err := hostconfig.CompanionHelperDigest(source)
	if err != nil {
		return setupStep{}, false, err
	}
	if err := sudo.ensure(ctx, "install the /etc/hosts helper at "+hostconfig.InstalledHelperPath+" (root-owned; lets `farrow hosts install` publish node names)"); err != nil {
		return setupStep{}, false, err
	}
	for _, directory := range []string{"/opt", "/opt/farrow", "/opt/farrow/libexec"} {
		info, statErr := os.Lstat(directory)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return setupStep{}, false, fmt.Errorf("inspect privileged helper directory %s: %w", directory, statErr)
		}
		statistics, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || statistics.Uid != 0 || statistics.Gid != 0 || info.Mode().Perm()&0o022 != 0 {
			return setupStep{}, false, fmt.Errorf("refuse unsafe privileged helper directory: %s", directory)
		}
	}
	root := setupRootRunner(base)
	if _, directoryErr := root.Run(ctx, "/usr/bin/install", "-d", "-o", "root", "-g", "0", "-m", "0755", "/opt/farrow", "/opt/farrow/libexec"); directoryErr != nil {
		return setupStep{}, true, fmt.Errorf("prepare privileged helper directory: %w", directoryErr)
	}
	stageID, err := identity.NewUUID()
	if err != nil {
		return setupStep{}, true, err
	}
	staged := filepath.Join(filepath.Dir(hostconfig.InstalledHelperPath), ".farrow-hosts-helper.next-"+stageID)
	defer func() {
		if staged != "" {
			_, _ = root.Run(context.Background(), "/bin/rm", "-f", "--", staged)
		}
	}()
	progressItem := startProgress(ctx, stderr, "Installing the private hosts helper")
	result, installErr := root.Run(ctx, "/usr/bin/install", "-o", "root", "-g", "0", "-m", "0755", source, staged)
	progressItem.Stop(installErr)
	if installErr != nil {
		debugf(stderr, "hosts helper install exit=%d stderr=%q", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
		return setupStep{}, true, fmt.Errorf("install private hosts helper failed with exit code %d", result.ExitCode)
	}
	stagedDigest, stageErr := hostconfig.RootOwnedHelperDigest(staged)
	if stageErr != nil || stagedDigest != digest {
		if stageErr == nil {
			stageErr = errors.New("staged hosts helper digest differs from the packaged CLI companion")
		}
		return setupStep{}, true, fmt.Errorf("verify staged private hosts helper: %w", stageErr)
	}
	moveResult, moveErr := root.Run(ctx, "/bin/mv", "-f", "--", staged, hostconfig.InstalledHelperPath)
	if moveErr != nil {
		debugf(stderr, "hosts helper publish exit=%d stderr=%q", moveResult.ExitCode, strings.TrimSpace(string(moveResult.Stderr)))
		return setupStep{}, true, fmt.Errorf("publish private hosts helper failed with exit code %d", moveResult.ExitCode)
	}
	staged = ""
	installedDigest, verifyErr := hostconfig.InstalledHelperDigest()
	if verifyErr != nil || installedDigest != digest {
		if verifyErr == nil {
			verifyErr = errors.New("installed hosts helper digest changed")
		}
		return setupStep{Name: "hosts-helper", Status: "failed", Changed: true}, true, fmt.Errorf("verify private hosts helper: %w", verifyErr)
	}
	status := "installed"
	if targetExists {
		status = "updated"
	}
	return setupStep{Name: "hosts-helper", Status: status, Detail: installedDigest, Changed: true}, false, nil
}

func setupCheckIgnored(name string, private bool) bool {
	// Setup has already run the mode-aware network preflight with the exact
	// selected node addresses (and suppresses address probes for an active
	// lease). Doctor's generic all-address network view is informational here.
	if strings.HasPrefix(name, "network-") {
		return true
	}
	if private {
		return false
	}
	return name == "linux-network-owner" || name == "bridge-helper" || name == "private-bridge"
}

func verifySetup(ctx context.Context, private bool) ([]doctor.Check, error) {
	report := (doctor.Probe{}).Run(ctx)
	errorsFound := make([]string, 0)
	for _, check := range report.Checks {
		if check.Status == doctor.Error && !setupCheckIgnored(check.Name, private) {
			line := check.Name + ": " + check.Evidence
			if check.Fix != "" {
				line += "; " + check.Fix
			}
			errorsFound = append(errorsFound, line)
		}
	}
	if len(errorsFound) > 0 {
		return report.Checks, errors.New(strings.Join(errorsFound, "\n"))
	}
	return report.Checks, nil
}

func setupMutating(plan setuphost.DependencyPlan, selection setupSelection, report *netpreflight.Report) bool {
	if len(plan.Commands) > 0 || selection.Publish {
		return true
	}
	if selection.Mode == "private" {
		if _, err := hostconfig.InstalledHelperDigest(); err != nil {
			return true
		}
	}
	return report != nil && setupNeedsNetworkInstall(*report)
}

// setupNodesSummary renders "1 node: meta @ 10.10.10.10" or
// "4 nodes: meta @ 10.10.10.10 … node-3 @ 10.10.10.13".
func setupNodesSummary(resolved spec.Resolved) string {
	nodes := resolved.Nodes
	switch len(nodes) {
	case 0:
		return "no nodes"
	case 1:
		return fmt.Sprintf("1 node: %s @ %s", nodes[0].Name, nodes[0].Address)
	case 2:
		return fmt.Sprintf("2 nodes: %s @ %s, %s @ %s", nodes[0].Name, nodes[0].Address, nodes[1].Name, nodes[1].Address)
	default:
		first, last := nodes[0], nodes[len(nodes)-1]
		return fmt.Sprintf("%d nodes: %s @ %s … %s @ %s", len(nodes), first.Name, first.Address, last.Name, last.Address)
	}
}

func planRow(stderr io.Writer, label, format string, arguments ...any) {
	fmt.Fprintf(stderr, "  %s %s\n", styled(stderr, ansiDim, fmt.Sprintf("%-13s", label)), fmt.Sprintf(format, arguments...))
}

// printSetupPlan tells the user exactly what will happen, which parts need
// root and why, and where any download would come from — before the single
// confirmation prompt.
func printSetupPlan(stderr io.Writer, plan setuphost.DependencyPlan, selection setupSelection, report *netpreflight.Report, dryRun bool) {
	if dryRun {
		fmt.Fprintf(stderr, "%s setup plan (dry run, no changes)\n", styled(stderr, ansiCyan, "→"))
	} else {
		fmt.Fprintf(stderr, "%s setup plan\n", styled(stderr, ansiCyan, "→"))
	}
	sudoFor := make([]string, 0, 3)

	// config: which file defines the lab, and what it resolves to.
	if selection.Publish {
		planRow(stderr, "config", "create %s (%s template: %s)", selection.ConfigPath, selection.Profile, setupNodesSummary(selection.Resolved))
	} else if selection.ConfigPath != "" {
		planRow(stderr, "config", "use %s (%s)", selection.ConfigPath, setupNodesSummary(selection.Resolved))
	}

	if len(plan.Commands) == 0 {
		planRow(stderr, "dependencies", "ready (QEMU, qemu-img, UEFI firmware, OpenSSH)")
	} else {
		planRow(stderr, "dependencies", "install via %s: %s", plan.Manager, strings.Join(plan.Missing, ", "))
		sudoFor = append(sudoFor, "package installation ("+plan.Manager+")")
	}

	networkInstall := false
	if report != nil {
		action := "reuse installed"
		if !report.Ready {
			action = "blocked"
		} else if setupNeedsNetworkInstall(*report) {
			action = "install"
			networkInstall = true
		}
		mode := ""
		if report.Installation.Mode != "" {
			mode = " (vmnet " + report.Installation.Mode + " mode)"
		}
		planRow(stderr, "network", "%s %s%s — fixed guest IPs, host-reachable", action, report.CIDR, mode)
	} else {
		planRow(stderr, "network", "install %s after dependencies — fixed guest IPs, host-reachable", selection.Resolved.Private.CIDR)
		networkInstall = true
	}
	if networkInstall {
		if runtime.GOOS == "darwin" {
			switch {
			case os.Getenv("FARROW_VMNET_ARCHIVE") != "":
				fmt.Fprintf(stderr, "                backend socket_vmnet %s from FARROW_VMNET_ARCHIVE (digest-verified)\n", darwinnet.ReleaseVersion)
			case setuphost.SocketVMNetCached(runtime.GOARCH):
				fmt.Fprintf(stderr, "                backend socket_vmnet %s already cached and verified; no download\n", darwinnet.ReleaseVersion)
			default:
				if _, ok := (darwinnet.HomebrewProbe{}).Brew(); ok {
					fmt.Fprintf(stderr, "                backend socket_vmnet %s via Homebrew (brew install %s), copied into root-owned /opt/farrow\n", darwinnet.ReleaseVersion, darwinnet.SocketVMNetFormula)
					fmt.Fprintln(stderr, "                fallback: digest-pinned download (FARROW_REPO mirror, github.com/lima-vm)")
				} else {
					fmt.Fprintf(stderr, "                downloads socket_vmnet %s (<4 MiB, SHA-256 pinned) from github.com/lima-vm\n", darwinnet.ReleaseVersion)
					fmt.Fprintln(stderr, "                mirrors: FARROW_REPO/<repo>/socket_vmnet/ or FARROW_VMNET_ARCHIVE=/path/to.tar.gz")
				}
			}
			sudoFor = append(sudoFor, "network service installation (socket_vmnet under /opt/farrow, root-owned)")
		} else {
			fmt.Fprintln(stderr, "                backend: farrow0 bridge via the active network manager; nothing is downloaded")
			sudoFor = append(sudoFor, "network installation (root-owned farrow0 bridge)")
		}
	}

	if _, err := hostconfig.InstalledHelperDigest(); err == nil {
		planRow(stderr, "hosts helper", "ready")
	} else {
		planRow(stderr, "hosts helper", "install %s — the narrow root-owned publisher behind `farrow hosts install`", hostconfig.InstalledHelperPath)
		sudoFor = append(sudoFor, "hosts-helper installation")
	}

	if len(sudoFor) == 0 {
		planRow(stderr, "privileges", "none; no root action in this plan")
	} else {
		planRow(stderr, "privileges", "sudo used for: %s", strings.Join(sudoFor, "; "))
		fmt.Fprintln(stderr, "                at most one password prompt; the first privileged step explains itself")
	}
}

func emitSetupResult(result setupResult, stdout, stderr io.Writer) int {
	if structuredOutput(stdout, false) {
		return encodeJSON(stdout, stderr, result)
	}
	textField(stdout, 14, "host", result.OS+"/"+result.Arch)
	if result.Profile != "" && result.Profile != "unknown" {
		textField(stdout, 14, "profile", result.Profile)
	}
	dependencyStatus := "ready"
	if result.DryRun && len(result.Dependencies.Commands) > 0 {
		dependencyStatus = "would install"
	} else {
		for _, step := range result.Steps {
			if step.Name == "dependencies" && step.Status == "installed" {
				dependencyStatus = "installed"
			}
		}
	}
	textField(stdout, 14, "dependencies", statusValue(stdout, dependencyStatus))
	if result.Network != nil {
		status := result.Network.Installation.Status
		if result.Blocked {
			status = "blocked"
		} else if status == "" || status == "absent" {
			status = "pending install"
		}
		textField(stdout, 14, "network", fmt.Sprintf("%s (%s)", result.Network.CIDR, status))
	} else {
		textField(stdout, 14, "network", fmt.Sprintf("%s (pending dependencies)", result.NetworkCIDR))
	}
	if result.NetworkMode != "" {
		textField(stdout, 14, "network mode", result.NetworkMode)
	}
	if result.Config != "" {
		textField(stdout, 14, "config", result.Config)
	}
	status := "ready"
	if result.Blocked {
		status = "blocked"
	} else if result.DryRun {
		status = "plan"
	}
	textField(stdout, 14, "status", statusValue(stdout, status))
	if result.Resolution != "" {
		textField(stdout, 14, "resolution", strings.ReplaceAll(result.Resolution, "\n", "; "))
	}
	if result.MutationUncertain {
		textField(stdout, 14, "mutation", "uncertain after a failed host command; inspect before retrying")
	}
	textField(stdout, 14, "next", result.Next)
	return exitOK
}

func failSetup(result *setupResult, code int, failure error, stdout, stderr io.Writer) int {
	result.Ready = false
	result.ExitCode = code
	result.Error = failure.Error()
	if result.Resolution == "" {
		result.Resolution = failure.Error()
		result.Next = "resolve the reported error, then rerun farrow setup"
		result.NextArgv = nil
	}
	errorf(stderr, "%v", failure)
	if structuredOutput(stdout, false) {
		if encodeCode := emitSetupResult(*result, stdout, stderr); encodeCode != exitOK {
			return encodeCode
		}
	}
	return code
}

func shellQuote(argument string) string {
	if argument == "" {
		return "''"
	}
	for _, character := range argument {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_@%+=:,./-", character) {
			continue
		}
		return "'" + strings.ReplaceAll(argument, "'", "'\\''") + "'"
	}
	return argument
}

func setupNextCommand(selection setupSelection, cwd string) (string, []string) {
	arguments := []string{"farrow", "up"}
	discoverable := false
	for _, name := range config.DiscoveryNames {
		if selection.ConfigPath == filepath.Join(cwd, name) {
			discoverable = true
		}
	}
	if selection.ConfigPath != "" && !discoverable {
		arguments = append(arguments, "-f", selection.ConfigPath)
	}
	return formatSetupCommand(arguments)
}

func setupApplyCommand(arguments []string, stdout, stderr io.Writer) (string, []string) {
	apply := []string{"farrow", "setup"}
	for _, argument := range arguments {
		if argument == "--dry-run" || argument == "-dry-run" ||
			strings.HasPrefix(argument, "--dry-run=") || strings.HasPrefix(argument, "-dry-run=") {
			continue
		}
		apply = append(apply, argument)
	}
	switch outputFormatFor(stdout) {
	case outputJSON:
		apply = append(apply, "--json")
	case outputYAML:
		apply = append(apply, "--yaml")
	}
	if verboseOutput(stderr) {
		apply = append(apply, "--verbose")
	}
	return formatSetupCommand(apply)
}

func formatSetupCommand(arguments []string) (string, []string) {
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		quoted[index] = shellQuote(argument)
	}
	return strings.Join(quoted, " "), arguments
}

func runSetupCommand(profileName string, options setupCLIOptions, stdout, stderr io.Writer) int {
	result := setupResult{
		Schema: 1, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Steps: make([]setupStep, 0, 6), NextArgv: nil,
	}
	if profileName != "" {
		result.Profile = profileName
	}
	result.DryRun = options.DryRun
	if options.DryRun && options.Yes {
		return failSetup(&result, exitUsage, errors.New("--dry-run and --yes are mutually exclusive"), stdout, stderr)
	}
	if options.Mode != "host" && options.Mode != "shared" {
		return failSetup(&result, exitUsage, errors.New("--mode must be host or shared"), stdout, stderr)
	}
	if runtime.GOOS != "darwin" && options.Mode != "host" {
		return failSetup(&result, exitUsage, errors.New("--mode shared is available only on Darwin"), stdout, stderr)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return failSetup(&result, exitRuntime, err, stdout, stderr)
	}
	selection, err := resolveSetupSelection(profileName, options.FilePath, options.NetworkCIDR, cwd)
	if err != nil {
		if strings.Contains(err.Error(), "unknown lab template") {
			return failSetup(&result, exitUsage, err, stdout, stderr)
		}
		return failSetup(&result, exitConflict, err, stdout, stderr)
	}
	result.Profile = selection.Profile
	result.Config = selection.ConfigPath
	next, nextArgv := setupNextCommand(selection, cwd)
	result.Next = next
	result.NextArgv = nextArgv
	private := selection.Mode == "private"
	if !private && options.ModeExplicit {
		return failSetup(&result, exitUsage, errors.New("--mode applies only to a private profile"), stdout, stderr)
	}
	dependencyPlan, err := setuphost.PlanDependencies(setuphost.DependencyProbe{}, private)
	if err != nil {
		return failSetup(&result, exitCapability, err, stdout, stderr)
	}
	result.Dependencies = dependencyPlan
	if dependencyPlan.Unsupported {
		return failSetup(&result, exitCapability, errors.New(dependencyPlan.Resolution), stdout, stderr)
	}
	base := execx.OSRunner{Timeout: 20 * time.Minute, OutputLimit: 64 << 10}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	sudoSession := &setupSudoSession{base: base, stderr: stderr}
	defer sudoSession.close()
	networkMode := ""
	if private {
		networkMode = "bridge"
		if runtime.GOOS == "darwin" {
			networkMode = options.Mode
		}
	}
	var networkReport *netpreflight.Report
	if private && dependencyPlan.Ready {
		report, reportErr := selectSetupNetwork(ctx, &selection, base)
		if reportErr != nil {
			return failSetup(&result, exitCapability, reportErr, stdout, stderr)
		}
		report, networkMode = constrainSetupNetworkMode(report, options.Mode, options.ModeExplicit)
		networkReport = &report
		result.Network = networkReport
		result.NetworkMode = networkMode
	}
	printSetupPlan(stderr, dependencyPlan, selection, networkReport, options.DryRun)
	result.Config = selection.ConfigPath
	result.Dependencies = dependencyPlan
	result.Network = networkReport
	result.NetworkMode = networkMode
	if private && selection.Resolved.Private != nil {
		result.NetworkCIDR = selection.Resolved.Private.CIDR
	}
	blockerCode := exitOK
	if networkReport != nil && !networkReport.Ready {
		result.Blocked = true
		result.Resolution = setupFindingError(*networkReport).Error()
		result.Next = "resolve the reported network conflict, then rerun farrow setup"
		result.NextArgv = nil
		blockerCode = networkReport.ExitCode
		if blockerCode == exitOK {
			blockerCode = exitConflict
		}
	}
	if options.DryRun {
		result.Applicable = !result.Blocked
		result.Ready = false
		result.Steps = append(result.Steps, setupStep{Name: "dependencies", Status: "planned"})
		if private {
			networkStatus := "planned"
			if result.Blocked {
				networkStatus = "blocked"
			}
			result.Steps = append(result.Steps, setupStep{Name: "network", Status: networkStatus})
			result.Steps = append(result.Steps, setupStep{Name: "hosts-helper", Status: "planned"})
		}
		if selection.Publish {
			result.Steps = append(result.Steps, setupStep{Name: "config", Status: "planned", Detail: selection.ConfigPath})
		}
		if !result.Blocked {
			result.Next, result.NextArgv = setupApplyCommand(options.arguments(profileName), stdout, stderr)
		}
		if result.Blocked {
			result.ExitCode = blockerCode
			errorf(stderr, "%s", result.Resolution)
		}
		if code := emitSetupResult(result, stdout, stderr); code != exitOK {
			return code
		}
		return blockerCode
	}
	if result.Blocked {
		result.ExitCode = blockerCode
		errorf(stderr, "%s", result.Resolution)
		if code := emitSetupResult(result, stdout, stderr); code != exitOK {
			return code
		}
		return blockerCode
	}
	if err := confirmSetup(options.Yes, setupMutating(dependencyPlan, selection, networkReport), os.Stdin, stderr); err != nil {
		return failSetup(&result, exitConflict, err, stdout, stderr)
	}
	if len(dependencyPlan.Commands) > 0 {
		changed, uncertain, err := runSetupCommands(ctx, dependencyPlan.Commands, base, sudoSession, stderr)
		result.Changed = result.Changed || changed
		result.MutationUncertain = result.MutationUncertain || uncertain
		if err != nil {
			result.Steps = append(result.Steps, setupStep{Name: "dependencies", Status: "failed", Changed: changed})
			return failSetup(&result, exitCapability, err, stdout, stderr)
		}
		result.Steps = append(result.Steps, setupStep{Name: "dependencies", Status: "installed", Changed: changed})
	} else {
		tickf(stderr, "Dependencies ready (QEMU, qemu-img, UEFI firmware, OpenSSH)")
		result.Steps = append(result.Steps, setupStep{Name: "dependencies", Status: "ready"})
	}
	verifiedDependencies, err := setuphost.PlanDependencies(setuphost.DependencyProbe{}, private)
	if err != nil || !verifiedDependencies.Ready {
		if err == nil {
			err = fmt.Errorf("dependencies remain unavailable: %s", strings.Join(verifiedDependencies.Missing, ", "))
		}
		return failSetup(&result, exitCapability, err, stdout, stderr)
	}
	result.Dependencies = verifiedDependencies
	capabilityProgress := startProgress(ctx, stderr, "Verifying native QEMU")
	capabilityChecks, capabilityErr := verifySetup(ctx, false)
	capabilityProgress.Stop(capabilityErr)
	result.Checks = capabilityChecks
	if capabilityErr != nil {
		result.Steps = append(result.Steps, setupStep{Name: "capabilities", Status: "failed"})
		return failSetup(&result, exitCapability, capabilityErr, stdout, stderr)
	}
	result.Steps = append(result.Steps, setupStep{Name: "capabilities", Status: "ready"})
	if private {
		report, reportErr := selectSetupNetwork(ctx, &selection, base)
		if reportErr != nil {
			return failSetup(&result, exitCapability, reportErr, stdout, stderr)
		}
		report, networkMode = constrainSetupNetworkMode(report, options.Mode, options.ModeExplicit)
		result.Network = &report
		result.NetworkMode = networkMode
		if selection.Resolved.Private != nil {
			result.NetworkCIDR = selection.Resolved.Private.CIDR
		}
		if !report.Ready {
			failure := setupFindingError(report)
			code := report.ExitCode
			if code == exitOK {
				code = exitCapability
			}
			result.Blocked = true
			result.Resolution = failure.Error()
			result.Next = "resolve the reported network conflict, then rerun farrow setup"
			result.NextArgv = nil
			return failSetup(&result, code, failure, stdout, stderr)
		}
		step, uncertain, installErr := applySetupNetwork(ctx, selection, networkMode, options.Repo, report, base, sudoSession, stderr)
		if installErr != nil {
			result.MutationUncertain = result.MutationUncertain || uncertain
			result.Steps = append(result.Steps, setupStep{Name: "network", Status: "failed"})
			if errors.Is(installErr, darwinnet.ErrVMNetSharingBusy) {
				return failSetup(&result, exitConflict, installErr, stdout, stderr)
			}
			return failSetup(&result, exitIntegrity, installErr, stdout, stderr)
		}
		result.Steps = append(result.Steps, step)
		result.Changed = result.Changed || step.Changed
		finalReport, finalErr := inspectSetupNetwork(ctx, selection, base)
		finalReport, _ = constrainSetupNetworkMode(finalReport, networkMode, true)
		if finalErr != nil || !finalReport.Ready || !finalReport.Installation.Healthy {
			if finalErr == nil {
				finalErr = setupFindingError(finalReport)
			}
			return failSetup(&result, exitIntegrity, fmt.Errorf("private network verification failed: %w", finalErr), stdout, stderr)
		}
		result.Network = &finalReport
		helperStep, uncertain, helperErr := ensureSetupHostsHelper(ctx, base, sudoSession, stderr)
		result.Changed = result.Changed || helperStep.Changed
		result.MutationUncertain = result.MutationUncertain || uncertain
		if helperErr != nil {
			if helperStep.Name == "" {
				helperStep = setupStep{Name: "hosts-helper", Status: "failed"}
			}
			result.Steps = append(result.Steps, helperStep)
			return failSetup(&result, exitIntegrity, helperErr, stdout, stderr)
		}
		result.Steps = append(result.Steps, helperStep)
	} else {
		result.Steps = append(result.Steps, setupStep{Name: "network", Status: "ready", Detail: "QEMU user NAT"})
	}
	if private {
		verifyProgress := startProgress(ctx, stderr, "Verifying the private host setup")
		checks, verifyErr := verifySetup(ctx, true)
		verifyProgress.Stop(verifyErr)
		result.Checks = checks
		if verifyErr != nil {
			result.Steps = append(result.Steps, setupStep{Name: "verify", Status: "failed"})
			return failSetup(&result, exitCapability, verifyErr, stdout, stderr)
		}
		result.Steps = append(result.Steps, setupStep{Name: "verify", Status: "ready"})
	}
	if selection.Publish {
		if len(bytes.TrimSpace(selection.ConfigData)) == 0 {
			return failSetup(&result, exitIntegrity, errors.New("generated setup configuration is empty"), stdout, stderr)
		}
		if err := fsutil.AtomicCreate(selection.ConfigPath, selection.ConfigData, 0o600); err != nil {
			code := exitIntegrity
			if errors.Is(err, os.ErrExist) {
				code = exitConflict
			}
			result.Steps = append(result.Steps, setupStep{Name: "config", Status: "failed", Detail: selection.ConfigPath})
			return failSetup(&result, code, fmt.Errorf("publish setup configuration without replacing concurrent content: %w", err), stdout, stderr)
		}
		result.Config = selection.ConfigPath
		result.Changed = true
		tickf(stderr, "Wrote %s", selection.ConfigPath)
		result.Steps = append(result.Steps, setupStep{Name: "config", Status: "created", Detail: selection.ConfigPath, Changed: true})
	} else if selection.ConfigPath != "" {
		result.Config = selection.ConfigPath
		tickf(stderr, "Using %s", selection.ConfigPath)
		result.Steps = append(result.Steps, setupStep{Name: "config", Status: "ready", Detail: selection.ConfigPath})
	}
	result.Applicable = true
	result.Ready = true
	return emitSetupResult(result, stdout, stderr)
}

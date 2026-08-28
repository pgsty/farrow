package private

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/diagnostics"
	"github.com/pgsty/farrow/internal/disk"
	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/lock"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/network/subnet"
	usernet "github.com/pgsty/farrow/internal/network/user"
	"github.com/pgsty/farrow/internal/openssh"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/sshkeys"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

type ImageResolver func(context.Context, string, string) (image.Entry, string, image.Metadata, error)
type ImageLookup func(context.Context, string, string) (image.Entry, error)
type HostPreflightFunc func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error)
type NetworkPreflightFunc func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report

type NetworkPreflightError struct{ Report netpreflight.Report }

var ErrRecreateRequired = errors.New("private desired spec requires recreate")

// ErrNodesRemoved reports nodes present in project state but absent from the
// desired configuration. The absence of a node from a configuration never
// implies destruction; removal is an explicit `farrow destroy <node> --force`.
var ErrNodesRemoved = errors.New("configuration no longer defines existing node(s)")

// resolvedDiff is the node-granular classification of a desired configuration
// against the applied project state.
type resolvedDiff struct {
	EnvelopeChanged bool
	Create          []string // desired nodes without committed state: new peers, or the per-node recreate window
	Changed         []string // desired nodes whose definition differs while their state still exists
	Removed         []string // stateful nodes the desired configuration dropped
	Unchanged       []string
}

func diffResolved(persisted, desired spec.Resolved, hasState func(string) bool) resolvedDiff {
	diff := resolvedDiff{}
	if !equalResolvedEnvelope(persisted, desired) {
		diff.EnvelopeChanged = true
	}
	persistedNodes := make(map[string]spec.Node, len(persisted.Nodes))
	for _, node := range persisted.Nodes {
		persistedNodes[node.Name] = node
	}
	desiredNames := make(map[string]struct{}, len(desired.Nodes))
	for _, node := range desired.Nodes {
		desiredNames[node.Name] = struct{}{}
		current, known := persistedNodes[node.Name]
		stateful := known && hasState(node.Name)
		switch {
		case !stateful:
			diff.Create = append(diff.Create, node.Name)
		case sameResolvedNode(current, node):
			diff.Unchanged = append(diff.Unchanged, node.Name)
		default:
			diff.Changed = append(diff.Changed, node.Name)
		}
	}
	for _, node := range persisted.Nodes {
		if _, kept := desiredNames[node.Name]; !kept && hasState(node.Name) {
			diff.Removed = append(diff.Removed, node.Name)
		}
	}
	return diff
}

func equalResolvedEnvelope(first, second spec.Resolved) bool {
	firstHash, firstErr := spec.Hash(envelopeOf(first))
	secondHash, secondErr := spec.Hash(envelopeOf(second))
	return firstErr == nil && secondErr == nil && firstHash == secondHash
}

func sameResolvedNode(first, second spec.Node) bool {
	firstData, firstErr := spec.CanonicalJSON(spec.Resolved{Nodes: []spec.Node{first}})
	secondData, secondErr := spec.CanonicalJSON(spec.Resolved{Nodes: []spec.Node{second}})
	return firstErr == nil && secondErr == nil && string(firstData) == string(secondData)
}

func (e *NetworkPreflightError) Error() string {
	for _, finding := range e.Report.Findings {
		if finding.Severity == netpreflight.Error {
			return finding.Code + ": " + finding.Evidence
		}
	}
	return "private network preflight failed"
}

type Manager struct {
	FarrowVersion       string
	OperationID         string
	Runner              execx.Runner
	ReadyTimeout        time.Duration
	Repository          string
	Progress            activity.Reporter
	NoWait              bool
	RollbackFailed      bool
	ResolveImage        ImageResolver
	LookupImage         ImageLookup
	HostPreflight       HostPreflightFunc
	NetworkPreflight    NetworkPreflightFunc
	ForceDarwinFD       bool
	NativeProfile       func() (platform.Profile, error)
	LookPath            func(string) (string, error)
	FirmwareLookup      func(platform.Profile, string) (platform.Firmware, error)
	DialSSHAddress      func(string, string) (net.Conn, error)
	Nodes               []string
	allowPartialDestroy bool
	dropFromSpec        bool
}

func (m Manager) report(phase, message string) {
	m.Progress.Report(activity.Event{Phase: phase, Message: message})
}

type NodeStatus struct {
	Name      string      `json:"name"`
	Address   string      `json:"address"`
	State     state.Phase `json:"state"`
	Runtime   string      `json:"runtime"`
	GuestArch string      `json:"guest_arch,omitempty"`
	Accel     string      `json:"accelerator,omitempty"`
	SSHHost   string      `json:"ssh_host"`
	SSHPort   uint16      `json:"ssh_port"`
	ProcessID int         `json:"pid,omitempty"`
}

func invocationOption(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func invocationGuestArch(binary string, arguments []string) string {
	switch filepath.Base(binary) {
	case "qemu-system-aarch64":
		return "arm64"
	case "qemu-system-x86_64":
		return "amd64"
	}
	machine := strings.SplitN(invocationOption(arguments, "-machine"), ",", 2)[0]
	switch machine {
	case "virt":
		return "arm64"
	case "q35":
		return "amd64"
	default:
		return ""
	}
}

type Status struct {
	OperationID string       `json:"operation_id,omitempty"`
	SpecHash    string       `json:"spec_hash"`
	Nodes       []NodeStatus `json:"nodes"`
	Message     string       `json:"message,omitempty"`
}

type Connection struct {
	Node       string `json:"node"`
	User       string `json:"user"`
	Host       string `json:"host"`
	Port       uint16 `json:"port"`
	PrivateKey string `json:"private_key"`
	KnownHosts string `json:"known_hosts"`
}

type LifecyclePlan struct {
	Schema      int      `json:"schema"`
	Action      string   `json:"action"`
	SpecHash    string   `json:"spec_hash"`
	Nodes       []string `json:"nodes"`
	Destructive bool     `json:"destructive"`
	Create      []string `json:"create,omitempty"`
	Recreate    []string `json:"recreate,omitempty"`
	Missing     []string `json:"missing,omitempty"`
}

func (m Manager) runner() execx.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return execx.OSRunner{Timeout: 15 * time.Second, OutputLimit: 1 << 20}
}

func (m Manager) nativeProfile() (platform.Profile, error) {
	if m.NativeProfile != nil {
		return m.NativeProfile()
	}
	return platform.Native()
}

func (m Manager) lookPath(name string) (string, error) {
	if m.LookPath != nil {
		return m.LookPath(name)
	}
	return exec.LookPath(name)
}

func (m Manager) firmwareForBoot(profile platform.Profile, boot string) (platform.Firmware, error) {
	if m.FirmwareLookup != nil {
		return m.FirmwareLookup(profile, boot)
	}
	return platform.FindFirmwareForBoot(profile, boot)
}

func (m Manager) readyTimeout(resolved spec.Resolved) (time.Duration, error) {
	if m.ReadyTimeout < 0 {
		return 0, errors.New("manager readiness timeout must be positive")
	}
	if m.ReadyTimeout > 0 {
		return m.ReadyTimeout, nil
	}
	return resolved.SSHWaitTimeout()
}

// nodesWithoutCommittedState filters to the nodes that have no committed
// state yet — the only ones whose fixed addresses must still be silent. A
// node this deployment already runs would otherwise probe as a conflict.
func nodesWithoutCommittedState(nodes []spec.Node) []spec.Node {
	root, err := state.ResolveDataRoot()
	if err != nil {
		return nodes
	}
	store := state.Store{Root: root}
	result := make([]spec.Node, 0, len(nodes))
	for _, node := range nodes {
		if _, readErr := store.ReadNode(node.Name); readErr == nil {
			continue
		}
		result = append(result, node)
	}
	return result
}

func boundedConcurrency(nodes int) int {
	if nodes < 1 {
		return 1
	}
	if nodes > 4 {
		return 4
	}
	return nodes
}

func selectedNodeNames(resolved spec.Resolved, requested []string) ([]string, error) {
	if len(requested) == 0 {
		result := make([]string, 0, len(resolved.Nodes))
		for _, node := range resolved.Nodes {
			result = append(result, node.Name)
		}
		return result, nil
	}
	known := make(map[string]struct{}, len(resolved.Nodes))
	for _, node := range resolved.Nodes {
		known[node.Name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	result := make([]string, 0, len(requested))
	for _, name := range requested {
		if _, ok := known[name]; !ok {
			return nil, fmt.Errorf("the deployment has no node %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("private node selection repeats %q", name)
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func nodeNameSet(names []string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func (m Manager) preflight(ctx context.Context, profile platform.Profile, resolved spec.Resolved) (Backend, error) {
	if resolved.Private == nil {
		return Backend{}, &CapabilityError{Reason: "private host preflight requires a resolved network"}
	}
	layout, layoutErr := subnet.Parse(resolved.Private.CIDR)
	if layoutErr != nil {
		return Backend{}, &CapabilityError{Reason: layoutErr.Error()}
	}
	addresses := make([]string, 0, len(resolved.Nodes))
	for _, node := range nodesWithoutCommittedState(resolved.Nodes) {
		addresses = append(addresses, node.Address)
	}
	request := netpreflight.Request{OS: profile.OS, Arch: profile.Arch, Purpose: netpreflight.Use, Layout: layout, Addresses: addresses}
	var report netpreflight.Report
	if m.NetworkPreflight != nil {
		report = m.NetworkPreflight(ctx, profile, request, m.runner())
	} else {
		report = netpreflight.Run(ctx, request, netpreflight.Probe{Runner: m.runner()})
	}
	if !report.Ready {
		return Backend{}, &NetworkPreflightError{Report: report}
	}
	var backend Backend
	var err error
	if m.HostPreflight != nil {
		backend, err = m.HostPreflight(ctx, profile, resolved.Private, m.runner())
	} else {
		backend, err = PreflightHost(ctx, profile, resolved.Private, m.runner())
	}
	if err != nil {
		return Backend{}, err
	}
	if m.ForceDarwinFD {
		if profile.OS != "darwin" || backend.DarwinSocket == "" {
			return Backend{}, errors.New("forced Darwin FD fallback requires a Darwin socket backend")
		}
		backend.DarwinUseFD = true
		backend.ReconnectMS = 0
	}
	return backend, nil
}

func missingPath(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var pathError *os.PathError
	return errors.As(err, &pathError) && errors.Is(pathError.Err, os.ErrNotExist)
}

func (m Manager) openProject(create bool) (Deployment, error) {
	return openDeployment(create)
}

func cloneResolved(value spec.Resolved) spec.Resolved {
	result := value
	if value.Private != nil {
		privateNetwork := *value.Private
		result.Private = &privateNetwork
	}
	result.Nodes = make([]spec.Node, len(value.Nodes))
	for index, node := range value.Nodes {
		result.Nodes[index] = node
		result.Nodes[index].Aliases = append([]string(nil), node.Aliases...)
		result.Nodes[index].Disks = append([]spec.Disk(nil), node.Disks...)
		result.Nodes[index].Forwards = append([]spec.Forward(nil), node.Forwards...)
		result.Nodes[index].Shares = append([]spec.Share(nil), node.Shares...)
	}
	return result
}

// materializeExistingForwardPorts preserves the finite host-port choices that
// were committed on first create. Without this normalization, a preferred
// port collision makes the original YAML look like destructive drift forever.
func materializeExistingForwardPorts(desired, existing spec.Resolved) spec.Resolved {
	result := cloneResolved(desired)
	existingNodes := make(map[string]spec.Node, len(existing.Nodes))
	for _, node := range existing.Nodes {
		existingNodes[node.Name] = node
	}
	for nodeIndex := range result.Nodes {
		current, ok := existingNodes[result.Nodes[nodeIndex].Name]
		if !ok {
			continue
		}
		result.Nodes[nodeIndex].Forwards = spec.ReuseMaterializedForwardPorts(result.Nodes[nodeIndex].Forwards, current.Forwards)
	}
	return result
}

func materializePorts(value spec.Resolved, reserved map[uint16]struct{}) (spec.Resolved, map[string]uint16, error) {
	return materializePortsWithProbe(value, reserved, func(bind string, port uint16) bool {
		listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(int(port))))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	})
}

func materializeMissingNodePorts(value spec.Resolved, createNames []string, reserved map[uint16]struct{}, store state.Store) (spec.Resolved, map[string]uint16, error) {
	if len(createNames) == 0 {
		return materializePorts(value, reserved)
	}
	createSet := nodeNameSet(createNames)
	ports := make(map[string]uint16, len(value.Nodes))
	available := func(bind string, port uint16) bool {
		if _, exists := reserved[port]; exists {
			return false
		}
		listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(int(port))))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}
	for _, definition := range value.Nodes {
		if _, create := createSet[definition.Name]; create {
			for _, forward := range definition.Forwards {
				if !available(forward.Bind, forward.Host) {
					return spec.Resolved{}, nil, fmt.Errorf("partial recreate requires exact forward %s:%d for node %s, but it is unavailable", forward.Bind, forward.Host, definition.Name)
				}
				reserved[forward.Host] = struct{}{}
			}
			continue
		}
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return spec.Resolved{}, nil, err
		}
		ports[definition.Name] = node.SSHPort
	}
	for index, definition := range value.Nodes {
		if _, create := createSet[definition.Name]; !create {
			continue
		}
		port, err := usernet.Choose(uint16(2222+index), func(port uint16) bool { return available("127.0.0.1", port) })
		if err != nil {
			return spec.Resolved{}, nil, fmt.Errorf("allocate management SSH for recreated node %s: %w", definition.Name, err)
		}
		reserved[port] = struct{}{}
		ports[definition.Name] = port
	}
	return cloneResolved(value), ports, nil
}

func materializePortsWithProbe(value spec.Resolved, reserved map[uint16]struct{}, probe func(string, uint16) bool) (spec.Resolved, map[string]uint16, error) {
	resolved := cloneResolved(value)
	selected := make(map[uint16]struct{})
	available := func(bind string) func(uint16) bool {
		return func(port uint16) bool {
			if _, exists := reserved[port]; exists {
				return false
			}
			if _, exists := selected[port]; exists {
				return false
			}
			return probe(bind, port)
		}
	}
	sshPorts := make(map[string]uint16, len(resolved.Nodes))
	for index := range resolved.Nodes {
		preferred := uint16(2222 + index)
		port, err := usernet.Choose(preferred, available("127.0.0.1"))
		if err != nil {
			return spec.Resolved{}, nil, fmt.Errorf("allocate management SSH for %s: %w", resolved.Nodes[index].Name, err)
		}
		selected[port] = struct{}{}
		sshPorts[resolved.Nodes[index].Name] = port
		for forwardIndex := range resolved.Nodes[index].Forwards {
			forward := &resolved.Nodes[index].Forwards[forwardIndex]
			port, err := usernet.Choose(spec.RequestedHostPort(*forward), available(forward.Bind))
			if err != nil {
				return spec.Resolved{}, nil, fmt.Errorf("allocate forward for %s: %w", resolved.Nodes[index].Name, err)
			}
			selected[port] = struct{}{}
			*forward = spec.WithMaterializedHost(*forward, port)
		}
	}
	return resolved, sshPorts, nil
}

func aliases(value spec.Resolved) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, node := range value.Nodes {
		alias := node.Image
		if alias == "" {
			alias = value.Image
		}
		if _, exists := seen[alias]; !exists {
			seen[alias] = struct{}{}
			result = append(result, alias)
		}
	}
	return result
}

func ensureSSHAddressesUnused(nodes []spec.Node) error {
	return ensureSSHAddressesUnusedWithDial(nodes, func(network, address string) (net.Conn, error) {
		return net.DialTimeout(network, address, 500*time.Millisecond)
	})
}

func (m Manager) ensureSSHAddressesUnused(nodes []spec.Node) error {
	if m.DialSSHAddress != nil {
		return ensureSSHAddressesUnusedWithDial(nodes, m.DialSSHAddress)
	}
	return ensureSSHAddressesUnused(nodes)
}

func ensureSSHAddressesUnusedWithDial(nodes []spec.Node, dial func(string, string) (net.Conn, error)) error {
	for _, node := range nodes {
		connection, err := dial("tcp", net.JoinHostPort(node.Address, "22"))
		if err != nil {
			continue
		}
		_ = connection.Close()
		return &CapabilityError{Reason: fmt.Sprintf("private address %s for node %s already accepts SSH before start; stop the conflicting VM or service", node.Address, node.Name)}
	}
	return nil
}

func (m Manager) imageDataRoot() (string, error) {
	return state.ResolveDataRoot()
}

func (m Manager) resolveOneImage(ctx context.Context, alias, arch string) (image.Entry, string, image.Metadata, error) {
	if m.ResolveImage != nil {
		return m.ResolveImage(ctx, alias, arch)
	}
	dataRoot, err := m.imageDataRoot()
	if err != nil {
		return image.Entry{}, "", image.Metadata{}, err
	}
	resolver := image.Service{DataRoot: dataRoot, Repository: m.Repository, Runner: m.Runner, Progress: m.Progress}
	return resolver.ResolveArch(ctx, alias, arch)
}

func (m Manager) lookupOneImage(ctx context.Context, alias, arch string) (image.Entry, error) {
	if m.LookupImage != nil {
		return m.LookupImage(ctx, alias, arch)
	}
	dataRoot, err := m.imageDataRoot()
	if err != nil {
		return image.Entry{}, err
	}
	resolver := image.Service{DataRoot: dataRoot, Repository: m.Repository, Runner: m.Runner, Progress: m.Progress}
	return resolver.LookupArch(ctx, alias, arch)
}

func mergeBootMode(profile platform.Profile, current, alias, candidate string) (string, error) {
	if current != "" && current != candidate {
		return "", errors.New("private v1 does not mix BIOS and UEFI images in one deployment")
	}
	if profile.RequiresUEFI && candidate != "uefi" {
		return "", &CapabilityError{Reason: fmt.Sprintf("image %s boot=%s is incompatible with required UEFI guest architecture", alias, candidate)}
	}
	return candidate, nil
}

func (m Manager) resolveBases(ctx context.Context, profile platform.Profile, value spec.Resolved) (map[string]BaseImage, string, error) {
	bases := make(map[string]BaseImage)
	boot := ""
	for _, alias := range aliases(value) {
		entry, path, metadata, err := m.resolveOneImage(ctx, alias, profile.Arch)
		if err != nil {
			return nil, "", err
		}
		boot, err = mergeBootMode(profile, boot, alias, entry.Boot)
		if err != nil {
			return nil, "", err
		}
		bases[entry.Alias] = BaseImage{Path: path, Alias: entry.Alias, Release: entry.Release, Digest: entry.SHA256, VirtualSize: metadata.VirtualSize}
		if alias != entry.Alias {
			bases[alias] = bases[entry.Alias]
		}
	}
	return bases, boot, nil
}

func (m Manager) resolveBootMode(ctx context.Context, profile platform.Profile, value spec.Resolved) (string, error) {
	boot := ""
	for _, alias := range aliases(value) {
		entry, err := m.lookupOneImage(ctx, alias, profile.Arch)
		if err != nil {
			return "", err
		}
		boot, err = mergeBootMode(profile, boot, alias, entry.Boot)
		if err != nil {
			return "", err
		}
	}
	return boot, nil
}

type runtimeDecision struct {
	Profile platform.Profile
	Reason  string
}

func selectRuntime(host platform.Profile, value spec.Resolved) (runtimeDecision, error) {
	guestArch, err := platform.GuestArch(value.Arch, host.Arch)
	if err != nil {
		return runtimeDecision{}, err
	}
	imageNames := aliases(value)
	if len(imageNames) == 0 && value.Image != "" {
		imageNames = []string{value.Image}
	}
	hasEL7, hasEL8 := false, false
	for _, name := range imageNames {
		canonical := image.CanonicalAlias(name)
		hasEL7 = hasEL7 || canonical == "el7"
		hasEL8 = hasEL8 || canonical == "el8"
	}
	if hasEL7 && (host.OS != "linux" || host.Arch != "amd64" || guestArch != "amd64") {
		return runtimeDecision{}, &CapabilityError{Reason: "EL7 is deliberately supported only on native Linux/amd64"}
	}
	emulate := guestArch != host.Arch
	reason := "guest architecture differs from the host"
	if hasEL8 && host.OS == "darwin" && host.Arch == "arm64" && guestArch == "arm64" {
		emulate = true
		reason = "stock EL8 arm64 requires a 64K translation granule unavailable through Apple HVF"
	}
	profile, err := platform.ResolveRuntime(host, guestArch, emulate)
	if err != nil {
		return runtimeDecision{}, err
	}
	if !profile.Emulated {
		reason = ""
	}
	return runtimeDecision{Profile: profile, Reason: reason}, nil
}

func (m Manager) resolveRuntimeQEMU(ctx context.Context, profile platform.Profile, backend Backend) (string, Backend, error) {
	qemuPath, err := platform.FindQEMUBinary(profile, m.lookPath)
	if err != nil {
		return "", Backend{}, err
	}
	if !profile.Emulated {
		return qemuPath, backend, nil
	}
	result, err := m.runner().Run(ctx, qemuPath, "--version")
	if err != nil {
		return "", Backend{}, fmt.Errorf("probe selected QEMU version: %w", err)
	}
	version, err := platform.ValidateQEMUVersion(profile, string(result.Stdout)+string(result.Stderr))
	if err != nil {
		return "", Backend{}, err
	}
	if profile.OS != "darwin" {
		return qemuPath, backend, nil
	}
	if backend.DarwinSocket == "" {
		return "", Backend{}, errors.New("selected Darwin QEMU runtime has no socket_vmnet backend")
	}
	help, err := m.runner().Run(ctx, qemuPath, "-machine", "none", "-netdev", "help")
	if err != nil {
		return "", Backend{}, fmt.Errorf("probe selected QEMU network backend: %w", err)
	}
	selected := selectDarwinBackend(version, string(help.Stdout)+string(help.Stderr), backend.DarwinSocket)
	selected.NetworkCIDR, selected.HostAddress, selected.DHCPEnd = backend.NetworkCIDR, backend.HostAddress, backend.DHCPEnd
	if backend.DarwinUseFD {
		selected.DarwinUseFD = true
		selected.ReconnectMS = 0
	}
	return qemuPath, selected, nil
}

func runtimeDriftNodes(store state.Store, resolved spec.Resolved, expected platform.Profile) ([]string, error) {
	drifted := make([]string, 0)
	for _, definition := range resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if missingPath(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		arch := invocationGuestArch(node.Invocation.Binary, node.Invocation.Args)
		accel := invocationOption(node.Invocation.Args, "-accel")
		if arch != expected.Arch || accel != expected.Accelerator {
			drifted = append(drifted, definition.Name)
		}
	}
	return drifted, nil
}

func (m Manager) ensureKeys(ctx context.Context, projectValue Deployment) (string, string, string, error) {
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := acquireDeploymentLock(lockContext, projectValue.Root, false)
	if err != nil {
		return "", "", "", err
	}
	defer projectLock.Release()
	return sshkeys.EnsureKeys(ctx, m.runner(), projectValue.Root)
}

func (m Manager) statusForLocked(ctx context.Context, projectValue Deployment, message string) (Status, error) {
	store := state.Store{Root: projectValue.Root}
	projectState, err := store.ReadDeployment()
	if err != nil {
		return Status{}, err
	}
	if projectState.Resolved.Network != "private" {
		return Status{}, errors.New("the deployment state is not private")
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	selectedSet := nodeNameSet(selected)
	result := Status{OperationID: m.OperationID, SpecHash: projectState.SpecHash, Message: message, Nodes: make([]NodeStatus, 0, len(projectState.Resolved.Nodes))}
	lifecycle := vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: projectState.Resolved.SSHUser}
	nodes := make([]state.NodeState, 0, len(projectState.Resolved.Nodes))
	convergenceCandidates := make([]state.NodeState, 0)
	for _, definition := range projectState.Resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		runtimeState := "inactive"
		if node.Phase == state.Running {
			identity := process.Identity{PID: node.Process.PID, Executable: node.Process.Executable, Started: node.Process.Started, ArgvHash: node.Process.ArgvHash}
			qmpErr := lifecycle.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID)
			if err := ctx.Err(); err != nil {
				return Status{}, err
			}
			processMatches := process.MatchesLive(ctx, m.runner(), identity, node.Invocation)
			if err := ctx.Err(); err != nil {
				return Status{}, err
			}
			switch {
			case qmpErr == nil && processMatches:
				runtimeState = "running"
			case qmpErr == nil:
				return Status{}, fmt.Errorf("private node %s has matching QMP but its recorded process identity does not match; recreate --force it", node.Node)
			case errors.Is(qmpErr, vm.ErrQMPIdentityMismatch):
				return Status{}, fmt.Errorf("private node %s has mismatched QMP identity; recreate --force it: %w", node.Node, qmpErr)
			case processMatches:
				return Status{}, fmt.Errorf("private node %s process identity matches but QMP is unavailable; run `farrow stop` to converge it", node.Node)
			case node.Runtime.Directory == "" || node.Runtime.QMP == "" || node.Runtime.PIDFile == "":
				return Status{}, fmt.Errorf("private node %s is recorded running with incomplete runtime identity; recreate is required", node.Node)
			case !completeProcess(node.Process):
				return Status{}, fmt.Errorf("private node %s is recorded running with incomplete process identity; recreate is required", node.Node)
			case process.Alive(node.Process.PID):
				return Status{}, fmt.Errorf("private node %s recorded PID %d is alive but full process identity does not match; verify and stop it manually", node.Node, node.Process.PID)
			default:
				// ValidateIdentity alone cannot distinguish a wholly stale QMP
				// socket from a live endpoint whose name responded but UUID query
				// failed. Require the stricter runtime auditor to prove the whole
				// QMP/process/pidfile identity dead before converging.
				observation, auditErr := RuntimeIdentityAuditor(m.runner(), time.Second)(ctx, node)
				if auditErr != nil {
					return Status{}, fmt.Errorf("private node %s runtime death audit is inconclusive: %w", node.Node, auditErr)
				}
				if observation.Live || observation.Authority != "dead" {
					return Status{}, fmt.Errorf("private node %s runtime became live during death audit: %s", node.Node, observation.Evidence)
				}
				// This is the safe self-halt case. Converge durable state without
				// touching stale runtime files.
				node.Phase = state.Stopped
				node.Process = state.ProcessIdentity{}
				node.UpdatedAt = time.Now().UTC()
				convergenceCandidates = append(convergenceCandidates, node)
			}
		} else if node.Phase == state.Stopping || node.Phase == state.Starting || node.Phase == state.Destroying {
			// An interrupted transition (a killed CLI mid-stop/start/destroy).
			// Prove the runtime dead before converging; a live runtime is
			// reported honestly and finished by `farrow stop`.
			observation, auditErr := RuntimeIdentityAuditor(m.runner(), time.Second)(ctx, node)
			if auditErr != nil {
				return Status{}, fmt.Errorf("private node %s was interrupted mid-%s and its runtime death audit is inconclusive: %w", node.Node, node.Phase, auditErr)
			}
			if observation.Live {
				runtimeState = "running"
			} else {
				node.Phase = state.Stopped
				node.Process = state.ProcessIdentity{}
				node.UpdatedAt = time.Now().UTC()
				convergenceCandidates = append(convergenceCandidates, node)
			}
		}
		nodes = append(nodes, node)
		if _, include := selectedSet[node.Node]; include {
			result.Nodes = append(result.Nodes, NodeStatus{
				Name: node.Node, Address: definition.Address, State: node.Phase, Runtime: runtimeState,
				GuestArch: invocationGuestArch(node.Invocation.Binary, node.Invocation.Args), Accel: invocationOption(node.Invocation.Args, "-accel"),
				SSHHost: "127.0.0.1", SSHPort: node.SSHPort, ProcessID: node.Process.PID,
			})
		}
	}
	for _, node := range convergenceCandidates {
		if err := store.WriteNode(node); err != nil {
			return Status{}, err
		}
	}
	_ = nodes
	return result, nil
}

func (m Manager) statusFor(ctx context.Context, projectValue Deployment, message string) (Status, error) {
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := acquireDeploymentLock(lockContext, projectValue.Root, false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	return m.statusForLocked(ctx, projectValue, message)
}

func (m Manager) Status(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	return m.statusFor(ctx, projectValue, "deployment status")
}

func (m Manager) Connection(ctx context.Context, requestedNode string) (Connection, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Connection{}, err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if err != nil || projectState.Resolved.Network != "private" {
		return Connection{}, errors.New("the deployment has no valid private state")
	}
	if requestedNode == "" {
		for _, node := range projectState.Resolved.Nodes {
			if node.Control {
				requestedNode = node.Name
				break
			}
		}
		if requestedNode == "" && len(projectState.Resolved.Nodes) > 0 {
			requestedNode = projectState.Resolved.Nodes[0].Name
		}
	}
	knownNode := false
	for _, node := range projectState.Resolved.Nodes {
		knownNode = knownNode || node.Name == requestedNode
	}
	if !knownNode {
		return Connection{}, fmt.Errorf("the deployment has no node %q", requestedNode)
	}
	status, err := m.statusFor(ctx, projectValue, "")
	if err != nil {
		return Connection{}, err
	}
	port := uint16(0)
	for _, node := range status.Nodes {
		if node.Name == requestedNode {
			if node.State != state.Running || node.Runtime != "running" {
				return Connection{}, fmt.Errorf("private node %s is not running", requestedNode)
			}
			port = node.SSHPort
		}
	}
	privateKey, knownHosts, err := validateSSHArtifacts(projectValue)
	if err != nil {
		return Connection{}, err
	}
	for path, mode := range map[string]os.FileMode{privateKey: 0o600, knownHosts: 0o600} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
			return Connection{}, fmt.Errorf("private SSH artifact is unsafe: %s", path)
		}
	}
	return Connection{Node: requestedNode, User: projectState.Resolved.SSHUser, Host: "127.0.0.1", Port: port, PrivateKey: privateKey, KnownHosts: knownHosts}, nil
}

func (m Manager) LogPath(nodeName, source string) (string, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return "", err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if err != nil || projectState.Resolved.Network != "private" {
		return "", errors.New("the deployment has no valid private state")
	}
	if source == "events" {
		path := filepath.Join(projectValue.Root, "events.jsonl")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("private event log is missing or unsafe: %s", path)
		}
		return path, nil
	}
	if source != "serial" && source != "qemu" {
		return "", fmt.Errorf("unsupported private log source %q", source)
	}
	if nodeName == "" {
		for _, node := range projectState.Resolved.Nodes {
			if node.Control {
				nodeName = node.Name
				break
			}
		}
	}
	nodeDir, err := projectValue.NodeDir(nodeName)
	if err != nil {
		return "", err
	}
	known := false
	for _, node := range projectState.Resolved.Nodes {
		known = known || node.Name == nodeName
	}
	if !known {
		return "", fmt.Errorf("the deployment has no node %q", nodeName)
	}
	logName := "serial.log"
	if source == "qemu" {
		logName = "qemu.log"
	}
	path := filepath.Join(nodeDir, logName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("private %s log is missing or unsafe: %s", source, path)
	}
	return path, nil
}

func (m Manager) SSHConfig(ctx context.Context) (string, error) {
	projectValue, projectState, nodes, err := m.integrationSnapshot(ctx)
	if err != nil {
		return "", err
	}
	privateKey, knownHosts, err := validateSSHArtifacts(projectValue)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	quotedIdentity, err := openssh.QuoteConfigValue(privateKey)
	if err != nil {
		return "", err
	}
	quotedKnownHosts, err := openssh.QuoteConfigValue(knownHosts)
	if err != nil {
		return "", err
	}
	for index, definition := range projectState.Resolved.Nodes {
		node := nodes[index]
		hostPatterns := []string{definition.Name}
		if definition.Address != "" {
			hostPatterns = append(hostPatterns, definition.Address)
		}
		hostPatterns = append(hostPatterns, definition.Aliases...)
		fmt.Fprintf(&output, "Host %s\n  HostName 127.0.0.1\n  User %s\n  Port %d\n  IdentityFile %s\n  UserKnownHostsFile %s\n  IdentitiesOnly yes\n  StrictHostKeyChecking yes\n\n", strings.Join(hostPatterns, " "), projectState.Resolved.SSHUser, node.SSHPort, quotedIdentity, quotedKnownHosts)
	}
	return output.String(), nil
}

func (m Manager) RecordEvent(ctx context.Context, action, level, message string) error {
	if m.OperationID == "" {
		return errors.New("private event requires an operation ID")
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return err
	}
	event := diagnostics.Event{Schema: 1, Time: time.Now().UTC(), Level: level, Node: "deployment", OperationID: m.OperationID, Action: action, Message: message}
	if err := diagnostics.AppendEvent(ctx, filepath.Join(projectValue.Root, "events.jsonl"), event); err != nil {
		return err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if err != nil {
		return nil
	}
	store := state.Store{Root: projectValue.Root}
	for _, definition := range projectState.Resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			continue
		}
		record := diagnostics.QEMULogRecord{Schema: 1, Time: time.Now().UTC(), Level: level, Node: node.Node, OperationID: m.OperationID, Action: action, Message: message + "; " + execx.Display(node.Invocation.Binary, node.Invocation.Args...)}
		if err := diagnostics.AppendQEMULog(ctx, filepath.Join(filepath.Dir(node.RootDisk), "qemu.log"), record); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) Up(ctx context.Context, requested spec.Resolved) (Status, error) {
	m.report("preflight", "Checking the private-network and native QEMU capabilities")
	var err error
	if err := validateResolved(requested); err != nil {
		return Status{}, err
	}
	hostProfile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	backend, err := m.preflight(ctx, hostProfile, requested)
	if err != nil {
		return Status{}, err
	}
	m.Progress.Report(activity.Event{Phase: "preflight", Message: "Private-network and QEMU preflight passed", Done: true})
	selected, err := selectedNodeNames(requested, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	selectedSet := nodeNameSet(selected)
	var reusableProject *Deployment
	createNodes := []string(nil)
	hadExistingState := false
	if existing, openErr := m.openProject(false); openErr == nil {
		persisted, err := (state.Store{Root: existing.Root}).ReadDeployment()
		if missingPath(err) {
			reusableProject = &existing
		} else if err != nil {
			return Status{}, err
		} else {
			hadExistingState = true
			requested = materializeExistingForwardPorts(requested, persisted.Resolved)
			store := state.Store{Root: existing.Root}
			hasState := func(name string) bool {
				_, readErr := store.ReadNode(name)
				return readErr == nil
			}
			diff := diffResolved(persisted.Resolved, requested, hasState)
			if diff.EnvelopeChanged {
				return Status{}, fmt.Errorf("%w: deployment-level settings changed; run farrow plan, then farrow recreate -f <config> --force", ErrRecreateRequired)
			}
			if len(diff.Removed) != 0 {
				return Status{}, fmt.Errorf("%w: %s; farrow never destroys from absence — run `farrow destroy %s --force` to remove them, or restore them in the configuration", ErrNodesRemoved, strings.Join(diff.Removed, ", "), strings.Join(diff.Removed, " "))
			}
			if len(diff.Changed) != 0 {
				return Status{}, fmt.Errorf("%w for node(s) %s; run farrow plan, then `farrow recreate --force %s` with this configuration", ErrRecreateRequired, strings.Join(diff.Changed, ", "), strings.Join(diff.Changed, " "))
			}
			allRunning, allRunnable := true, true
			for _, definition := range requested.Nodes {
				if _, include := selectedSet[definition.Name]; !include {
					continue
				}
				node, err := store.ReadNode(definition.Name)
				if missingPath(err) {
					createNodes = append(createNodes, definition.Name)
					allRunning = false
					continue
				}
				if err != nil {
					return Status{}, err
				}
				allRunning = allRunning && node.Phase == state.Running
				allRunnable = allRunnable && (node.Phase == state.Running || node.Phase == state.Stopped || node.Phase == state.Prepared)
			}
			if len(createNodes) != 0 {
				reusableProject = &existing
			} else if allRunning {
				status, err := m.statusFor(ctx, existing, "already running")
				if err != nil {
					return Status{}, err
				}
				if statusNodesRunning(status) {
					m.Progress.Report(activity.Event{Phase: "deployment-state", Message: "All selected private nodes are already running", Done: true})
					return status, nil
				}
				return m.startExisting(ctx, existing, persisted, hostProfile, backend)
			} else if allRunnable {
				return m.startExisting(ctx, existing, persisted, hostProfile, backend)
			} else {
				return Status{}, errors.New("the deployment has mixed node phases; run `farrow status` to converge interrupted transitions, then retry")
			}
		}
	} else if !missingPath(openErr) {
		return Status{}, openErr
	}
	if err := m.ensureSSHAddressesUnused(nodesWithoutCommittedState(requested.Nodes)); err != nil {
		return Status{}, err
	}
	runtime, err := selectRuntime(hostProfile, requested)
	if err != nil {
		return Status{}, err
	}
	if hadExistingState && reusableProject != nil {
		drifted, driftErr := runtimeDriftNodes(state.Store{Root: reusableProject.Root}, requested, runtime.Profile)
		if driftErr != nil {
			return Status{}, driftErr
		}
		if len(drifted) != 0 {
			return Status{}, fmt.Errorf("%w: existing node runtime differs for %s", ErrRecreateRequired, strings.Join(drifted, ", "))
		}
	}
	if runtime.Profile.Emulated {
		m.report("runtime", fmt.Sprintf("Using TCG compatibility mode for %s: %s; functional results are valid, performance results are not", runtime.Profile.Arch, runtime.Reason))
	}
	qemuPath, backend, err := m.resolveRuntimeQEMU(ctx, runtime.Profile, backend)
	if err != nil {
		return Status{}, err
	}
	m.report("image-resolve", fmt.Sprintf("Resolving images for %d node(s)", len(selected)))
	bases, boot, err := m.resolveBases(ctx, runtime.Profile, requested)
	if err != nil {
		return Status{}, err
	}
	if selectedHasShares(requested, selected) {
		if err := validatePrivateShareDeviceHelp(ctx, m.runner(), []string{qemuPath}); err != nil {
			return Status{}, err
		}
	}
	qemuImg, err := m.lookPath("qemu-img")
	if err != nil {
		return Status{}, err
	}
	sshPath, err := m.lookPath("ssh")
	if err != nil {
		return Status{}, err
	}
	projectValue := Deployment{}
	if reusableProject != nil {
		projectValue = *reusableProject
	} else {
		projectValue, err = m.openProject(true)
		if err != nil {
			return Status{}, err
		}
	}
	if err := selectedShareSources(projectValue, requested, selected); err != nil {
		return Status{}, err
	}
	if err := validatePrivatePersistentDesired(projectValue, requested); err != nil {
		return Status{}, err
	}
	allocatorContext, cancelAllocator := context.WithTimeout(ctx, 30*time.Second)
	defer cancelAllocator()
	allocator, err := lock.Acquire(allocatorContext, filepath.Join(projectValue.DataRoot, "locks", "allocator.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer allocator.Release()
	resolved, sshPorts, err := materializeMissingNodePorts(requested, createNodes, make(map[uint16]struct{}), state.Store{Root: projectValue.Root})
	if err != nil {
		return Status{}, err
	}
	specHash, err := spec.Hash(resolved)
	if err != nil {
		return Status{}, err
	}
	nodeHashes, err := spec.NodeHashes(resolved)
	if err != nil {
		return Status{}, err
	}
	privateKeyPath, knownHosts, publicKey, err := m.ensureKeys(ctx, projectValue)
	if err != nil {
		return Status{}, err
	}
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return Status{}, err
	}
	knownUUIDs := make(map[string]string)
	if hadExistingState {
		createSet := nodeNameSet(createNodes)
		store := state.Store{Root: projectValue.Root}
		for _, definition := range resolved.Nodes {
			if _, create := createSet[definition.Name]; create {
				continue
			}
			node, readErr := store.ReadNode(definition.Name)
			if readErr != nil {
				return Status{}, readErr
			}
			knownUUIDs[node.Node] = node.VMUUID
		}
	}
	plan, err := Build(resolved, os.Getuid(), knownUUIDs, nil)
	if err != nil {
		return Status{}, err
	}
	generations := make(map[string]uint64, len(resolved.Nodes))
	for _, node := range resolved.Nodes {
		generations[node.Name] = 1
	}
	seeds, err := RenderSeeds(resolved, plan, SeedInput{PublicKey: publicKey, PrivateKey: string(privateKey), SpecHashes: nodeHashes, Generation: generations})
	if err != nil {
		return Status{}, err
	}
	firmware, err := m.firmwareForBoot(runtime.Profile, boot)
	if err != nil {
		return Status{}, err
	}
	prepare := PrepareConfig{
		ProjectRoot: projectValue.Root, Resolved: resolved, SpecHash: specHash, NodeHashes: nodeHashes, Plan: plan, Seeds: seeds, Bases: bases, SSHPorts: sshPorts,
		Profile: runtime.Profile, QEMUBinary: qemuPath, Firmware: firmware, UseUEFI: boot == "uefi", Backend: backend,
		Disks: disk.Manager{QEMUImg: qemuImg, Runner: m.runner()},
	}
	lifecycle := NativeLifecycle{VM: vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: resolved.SSHUser}, Project: projectValue, Shares: shareSourcesByNode(resolved), SSHPath: sshPath, PrivateKey: privateKeyPath, KnownHosts: knownHosts, DarwinSocket: backend.DarwinSocket}
	readyTimeout, err := m.readyTimeout(resolved)
	if err != nil {
		return Status{}, err
	}
	startNodes := selected
	if len(createNodes) != 0 {
		startNodes = append([]string(nil), createNodes...)
	}
	controller := Controller{Project: projectValue, Prepare: prepare, Lifecycle: lifecycle, Concurrency: boundedConcurrency(len(resolved.Nodes)), ReadyTimeout: readyTimeout, NoWait: m.NoWait, CreateNodes: createNodes, StartNodes: startNodes, Version: m.FarrowVersion, Progress: m.Progress}
	createResult, err := controller.CreateAndStart(ctx)
	if err != nil {
		if m.RollbackFailed {
			err = rollbackCreateFailure(projectValue, createResult, err)
		}
		return Status{}, err
	}
	if len(createNodes) != 0 {
		projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
		if err != nil {
			return Status{}, err
		}
		return m.startExisting(ctx, projectValue, projectState, hostProfile, backend)
	}
	return m.statusFor(ctx, projectValue, "created and started the deployment")
}

func (m Manager) startExisting(ctx context.Context, projectValue Deployment, projectState state.DeploymentState, profile platform.Profile, backend Backend) (Status, error) {
	m.report("preflight", "Checking the existing deployment before start")
	verifiedBackend, err := m.preflight(ctx, profile, projectState.Resolved)
	if err != nil {
		return Status{}, err
	}
	backend = verifiedBackend
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	if err := selectedShareSources(projectValue, projectState.Resolved, selected); err != nil {
		return Status{}, err
	}
	preStartStore := state.Store{Root: projectValue.Root}
	shareBinaries, err := selectedShareInvocationBinaries(preStartStore, projectState.Resolved, selected)
	if err != nil {
		return Status{}, err
	}
	if err := validatePrivateShareDeviceHelp(ctx, m.runner(), shareBinaries); err != nil {
		return Status{}, err
	}
	selectedSet := nodeNameSet(selected)
	if _, err := m.statusFor(ctx, projectValue, "pre-start identity audit"); err != nil {
		return Status{}, err
	}
	startableDefinitions := make([]spec.Node, 0, len(projectState.Resolved.Nodes))
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		node, err := preStartStore.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		if node.Phase == state.Prepared || node.Phase == state.Stopped {
			startableDefinitions = append(startableDefinitions, definition)
		}
	}
	if err := m.ensureSSHAddressesUnused(startableDefinitions); err != nil {
		return Status{}, err
	}
	lockContext, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	projectLock, err := acquireDeploymentLock(lockContext, projectValue.Root, false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	sshPath, err := m.lookPath("ssh")
	if err != nil {
		return Status{}, err
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	lifecycle := NativeLifecycle{VM: vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: projectState.Resolved.SSHUser}, Project: projectValue, Shares: shareSourcesByNode(projectState.Resolved), SSHPath: sshPath, PrivateKey: filepath.Join(keysDir, "id_ed25519"), KnownHosts: filepath.Join(keysDir, "known_hosts"), DarwinSocket: backend.DarwinSocket}
	names := make([]string, 0, len(projectState.Resolved.Nodes))
	store := state.Store{Root: projectValue.Root}
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		switch node.Phase {
		case state.Running:
			continue
		case state.Prepared, state.Stopped:
			names = append(names, node.Node)
		default:
			return Status{}, fmt.Errorf("private node %s phase %s requires repair before start", node.Node, node.Phase)
		}
	}
	if len(names) == 0 {
		m.Progress.Report(activity.Event{Phase: "deployment-state", Message: "All selected private nodes are already running", Done: true})
		return m.statusForLocked(ctx, projectValue, "already running")
	}
	readyTimeout, err := m.readyTimeout(projectState.Resolved)
	if err != nil {
		return Status{}, err
	}
	startMessage := fmt.Sprintf("Starting %d private node(s) and waiting up to %s for guest readiness", len(names), readyTimeout)
	if m.NoWait {
		startMessage = fmt.Sprintf("Starting %d private node(s) without waiting for guest readiness", len(names))
	}
	m.report("guest-ready", startMessage)
	outcomes, err := StartPrepared(ctx, StartConfig{Project: projectValue, Lifecycle: lifecycle, Nodes: names, Concurrency: boundedConcurrency(len(names)), ReadyTimeout: readyTimeout, NoWait: m.NoWait})
	if err != nil {
		return Status{}, err
	}
	if failed := failedStartNames(outcomes); len(failed) > 0 {
		return Status{}, &PartialError{Nodes: failed}
	}
	readyMessage := fmt.Sprintf("All %d private node(s) are ready", len(names))
	if m.NoWait {
		readyMessage = fmt.Sprintf("QEMU is running for %d private node(s); guest readiness was skipped", len(names))
	}
	m.Progress.Report(activity.Event{Phase: "guest-ready", Message: readyMessage, Done: true})
	return m.statusForLocked(ctx, projectValue, "started the deployment")
}

func statusNodesRunning(status Status) bool {
	for _, node := range status.Nodes {
		if node.State != state.Running || node.Runtime != "running" {
			return false
		}
	}
	return len(status.Nodes) > 0
}

func failedStartNames(outcomes []StartOutcome) []string {
	failed := make([]string, 0)
	for _, outcome := range outcomes {
		if outcome.Error != "" || !outcome.Ready {
			failed = append(failed, outcome.Node)
		}
	}
	return failed
}

func (m Manager) Start(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("the deployment has no valid private state")
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	backend, err := m.preflight(ctx, profile, projectState.Resolved)
	if err != nil {
		return Status{}, err
	}
	return m.startExisting(ctx, projectValue, projectState, profile, backend)
}

func (m Manager) Stop(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("the deployment has no valid private state")
	}
	lockContext, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	projectLock, err := acquireDeploymentLock(lockContext, projectValue.Root, false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	names, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	lifecycle := NativeLifecycle{VM: vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: projectState.Resolved.SSHUser}}
	outcomes, err := StopRunning(ctx, StopConfig{Project: projectValue, Lifecycle: lifecycle, Nodes: names, Concurrency: boundedConcurrency(len(names))})
	if err != nil {
		return Status{}, err
	}
	failed := make([]string, 0)
	for _, outcome := range outcomes {
		if outcome.Error != "" || !outcome.Stopped {
			failed = append(failed, outcome.Node)
		}
	}
	if len(failed) > 0 {
		return Status{}, &PartialError{Nodes: failed}
	}
	return m.statusForLocked(ctx, projectValue, "stopped selected private nodes")
}

func (m Manager) Restart(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("the deployment has no valid private state")
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	if _, err := m.preflight(ctx, profile, projectState.Resolved); err != nil {
		return Status{}, err
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	if err := selectedShareSources(projectValue, projectState.Resolved, selected); err != nil {
		return Status{}, err
	}
	shareBinaries, err := selectedShareInvocationBinaries(state.Store{Root: projectValue.Root}, projectState.Resolved, selected)
	if err != nil {
		return Status{}, err
	}
	if err := validatePrivateShareDeviceHelp(ctx, m.runner(), shareBinaries); err != nil {
		return Status{}, err
	}
	if _, err := m.Stop(ctx); err != nil {
		return Status{}, err
	}
	return m.Start(ctx)
}

func (m Manager) Plan(ctx context.Context, requested spec.Resolved) (LifecyclePlan, error) {
	var err error
	if err := validateResolved(requested); err != nil {
		return LifecyclePlan{}, err
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return LifecyclePlan{}, err
	}
	runtime, err := selectRuntime(profile, requested)
	if err != nil {
		return LifecyclePlan{}, err
	}
	backend, err := m.preflight(ctx, profile, requested)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if runtime.Profile.Emulated {
		if _, _, err := m.resolveRuntimeQEMU(ctx, runtime.Profile, backend); err != nil {
			return LifecyclePlan{}, err
		}
		boot, err := m.resolveBootMode(ctx, runtime.Profile, requested)
		if err != nil {
			return LifecyclePlan{}, err
		}
		if _, err := m.firmwareForBoot(runtime.Profile, boot); err != nil {
			return LifecyclePlan{}, err
		}
	}
	hash, err := spec.Hash(requested)
	if err != nil {
		return LifecyclePlan{}, err
	}
	selected, err := selectedNodeNames(requested, m.Nodes)
	if err != nil {
		return LifecyclePlan{}, err
	}
	selectedSet := nodeNameSet(selected)
	result := LifecyclePlan{Schema: 1, Action: "create", SpecHash: hash, Nodes: append([]string(nil), selected...)}
	if err := m.ensureSSHAddressesUnused(nodesWithoutCommittedState(requested.Nodes)); err != nil {
		return LifecyclePlan{}, err
	}
	projectValue, err := m.openProject(false)
	if missingPath(err) {
		return result, nil
	}
	if err != nil {
		return LifecyclePlan{}, err
	}
	projectState, err := (state.Store{Root: projectValue.Root}).ReadDeployment()
	if missingPath(err) {
		return result, nil
	}
	if err != nil {
		return LifecyclePlan{}, err
	}
	requested = materializeExistingForwardPorts(requested, projectState.Resolved)
	hash, err = spec.Hash(requested)
	if err != nil {
		return LifecyclePlan{}, err
	}
	result.SpecHash = hash
	store := state.Store{Root: projectValue.Root}
	hasState := func(name string) bool {
		_, readErr := store.ReadNode(name)
		return readErr == nil
	}
	diff := diffResolved(projectState.Resolved, requested, hasState)
	result.Create = append([]string(nil), diff.Create...)
	result.Recreate = append([]string(nil), diff.Changed...)
	result.Missing = append([]string(nil), diff.Removed...)
	runtimeDrift, err := runtimeDriftNodes(store, requested, runtime.Profile)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if len(runtimeDrift) != 0 && len(m.Nodes) != 0 {
		return LifecyclePlan{}, fmt.Errorf("%w: runtime changes require whole-deployment plan without node selectors", ErrRecreateRequired)
	}
	switch {
	case diff.EnvelopeChanged:
		result.Action = "recreate"
		result.Destructive = true
		return result, nil
	case len(diff.Removed) != 0:
		result.Action = "blocked-removal"
		return result, nil
	case len(diff.Changed) != 0:
		result.Action = "recreate"
		result.Destructive = true
		return result, nil
	case len(runtimeDrift) != 0:
		result.Action = "recreate"
		result.Destructive = true
		result.Recreate = append([]string(nil), runtimeDrift...)
		return result, nil
	case len(diff.Create) != 0:
		result.Action = "converge"
		return result, nil
	}
	allRunning, allStartable := true, true
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return LifecyclePlan{}, err
		}
		allRunning = allRunning && node.Phase == state.Running
		allStartable = allStartable && (node.Phase == state.Prepared || node.Phase == state.Stopped)
	}
	switch {
	case allRunning:
		result.Action = "none"
	case allStartable:
		result.Action = "start"
	default:
		result.Action = "repair"
	}
	return result, nil
}

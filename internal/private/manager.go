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
	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/lock"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/network/subnet"
	usernet "github.com/pgsty/farrow/internal/network/user"
	"github.com/pgsty/farrow/internal/openssh"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/portregistry"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/quick"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

type ImageResolver func(context.Context, string) (image.Entry, string, image.Metadata, error)
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
	CWD                 string
	FarrowVersion       string
	OperationID         string
	Runner              execx.Runner
	LeaseStore          *lease.Store
	ReadyTimeout        time.Duration
	ConfiguredDataRoot  string
	Repository          string
	Progress            activity.Reporter
	NoWait              bool
	RollbackFailed      bool
	ResolveImage        ImageResolver
	HostPreflight       HostPreflightFunc
	NetworkPreflight    NetworkPreflightFunc
	ForceDarwinFD       bool
	NativeProfile       func() (platform.Profile, error)
	LookPath            func(string) (string, error)
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
	SSHHost   string      `json:"ssh_host"`
	SSHPort   uint16      `json:"ssh_port"`
	ProcessID int         `json:"pid,omitempty"`
}

type Status struct {
	ProjectID   string       `json:"project_id"`
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
	LeaseActive bool     `json:"lease_active"`
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

func (m Manager) leaseStore() lease.Store {
	if m.LeaseStore != nil {
		return *m.LeaseStore
	}
	return lease.Store{}
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

func (m Manager) readyTimeout(resolved spec.Resolved) (time.Duration, error) {
	if m.ReadyTimeout < 0 {
		return 0, errors.New("manager readiness timeout must be positive")
	}
	if m.ReadyTimeout > 0 {
		return m.ReadyTimeout, nil
	}
	return resolved.SSHWaitTimeout()
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
			return nil, fmt.Errorf("private project has no node %q", name)
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
	for _, node := range resolved.Nodes {
		addresses = append(addresses, node.Address)
	}
	if leaseStatus, leaseErr := m.leaseStore().Inspect(); leaseErr == nil && leaseStatus.Active {
		addresses = nil
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

func (m Manager) workDir() (string, error) {
	if m.CWD != "" {
		return filepath.Abs(m.CWD)
	}
	return os.Getwd()
}

func missingPath(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var pathError *os.PathError
	return errors.As(err, &pathError) && errors.Is(pathError.Err, os.ErrNotExist)
}

func (m Manager) openProject(create bool) (project.Project, error) {
	cwd, err := m.workDir()
	if err != nil {
		return project.Project{}, err
	}
	return project.OpenConfigured(cwd, m.ConfiguredDataRoot, create)
}

func (m Manager) materializeDataRoot(requested spec.Resolved) (spec.Resolved, error) {
	m.ConfiguredDataRoot = requested.DataRoot
	cwd, err := m.workDir()
	if err != nil {
		return spec.Resolved{}, err
	}
	if existing, openErr := m.openProject(false); openErr == nil {
		requested.DataRoot = existing.DataRoot
		return requested, nil
	} else if !missingPath(openErr) {
		return spec.Resolved{}, openErr
	}
	dataRoot, err := project.ResolveDataRootWithConfig(cwd, m.ConfiguredDataRoot, nil)
	if err != nil {
		return spec.Resolved{}, err
	}
	requested.DataRoot = dataRoot
	return requested, nil
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

func (m Manager) resolveOneImage(ctx context.Context, alias string) (image.Entry, string, image.Metadata, error) {
	if m.ResolveImage != nil {
		return m.ResolveImage(ctx, alias)
	}
	resolver := quick.Manager{CWD: m.CWD, FarrowVersion: m.FarrowVersion, Runner: m.Runner, ConfiguredDataRoot: m.ConfiguredDataRoot, Repository: m.Repository, Progress: m.Progress}
	return resolver.ResolveImage(ctx, alias)
}

func (m Manager) resolveBases(ctx context.Context, profile platform.Profile, value spec.Resolved) (map[string]BaseImage, string, error) {
	bases := make(map[string]BaseImage)
	boot := ""
	for _, alias := range aliases(value) {
		entry, path, metadata, err := m.resolveOneImage(ctx, alias)
		if err != nil {
			return nil, "", err
		}
		if boot == "" {
			boot = entry.Boot
		} else if boot != entry.Boot {
			return nil, "", errors.New("private v1 does not mix BIOS and UEFI images in one project")
		}
		if profile.RequiresUEFI && entry.Boot != "uefi" {
			return nil, "", &CapabilityError{Reason: fmt.Sprintf("image %s boot=%s is incompatible with required UEFI host profile", alias, entry.Boot)}
		}
		bases[entry.Alias] = BaseImage{Path: path, Alias: entry.Alias, Release: entry.Release, Digest: entry.SHA256, VirtualSize: metadata.VirtualSize}
		if alias != entry.Alias {
			bases[alias] = bases[entry.Alias]
		}
	}
	return bases, boot, nil
}

func (m Manager) ensureKeys(ctx context.Context, projectValue project.Project) (string, string, string, error) {
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return "", "", "", err
	}
	defer projectLock.Release()
	return quick.EnsureProjectKeys(ctx, m.runner(), projectValue.Root)
}

func (m Manager) statusForLocked(ctx context.Context, projectValue project.Project, message string) (Status, error) {
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil {
		return Status{}, err
	}
	if projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project is not private")
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	selectedSet := nodeNameSet(selected)
	result := Status{ProjectID: projectValue.Marker.ProjectID, OperationID: m.OperationID, SpecHash: projectState.SpecHash, Message: message, Nodes: make([]NodeStatus, 0, len(projectState.Resolved.Nodes))}
	lifecycle := vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: projectState.Resolved.SSHUser}
	nodes := make([]state.NodeState, 0, len(projectState.Resolved.Nodes))
	convergenceCandidates := make([]state.NodeState, 0)
	needsLeaseSync := false
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
				return Status{}, fmt.Errorf("private node %s has matching QMP but its recorded process identity does not match; repair is required", node.Node)
			case errors.Is(qmpErr, vm.ErrQMPIdentityMismatch):
				return Status{}, fmt.Errorf("private node %s has mismatched QMP identity; repair is required: %w", node.Node, qmpErr)
			case processMatches:
				return Status{}, fmt.Errorf("private node %s process identity matches but QMP is unavailable; repair is required", node.Node)
			case !completePrivateRuntimeIdentity(node):
				return Status{}, fmt.Errorf("private node %s is recorded running with incomplete runtime identity; repair is required", node.Node)
			case !completePrivateProcessIdentity(node):
				return Status{}, fmt.Errorf("private node %s is recorded running with incomplete process identity; repair is required", node.Node)
			case process.Alive(node.Process.PID):
				return Status{}, fmt.Errorf("private node %s recorded PID %d is alive but full process identity does not match; repair is required", node.Node, node.Process.PID)
			default:
				// ValidateIdentity alone cannot distinguish a wholly stale QMP
				// socket from a live endpoint whose name responded but UUID query
				// failed. Require the stricter runtime auditor to prove the whole
				// QMP/process/pidfile identity dead before converging.
				observation, auditErr := lease.RuntimeIdentityAuditor(m.runner(), time.Second)(ctx, privateLeaseNode(node))
				if auditErr != nil {
					return Status{}, fmt.Errorf("private node %s runtime death audit is inconclusive; repair is required: %w", node.Node, auditErr)
				}
				if observation.Live || observation.Authority != "dead" {
					return Status{}, fmt.Errorf("private node %s runtime became live during death audit; repair is required: %s", node.Node, observation.Evidence)
				}
				// This is the safe self-halt case. Converge durable state without
				// touching stale runtime files.
				node.Phase = state.Stopped
				node.Process = state.ProcessIdentity{}
				node.UpdatedAt = time.Now().UTC()
				convergenceCandidates = append(convergenceCandidates, node)
				needsLeaseSync = true
			}
		}
		needsLeaseSync = needsLeaseSync || node.Phase == state.Stopped
		nodes = append(nodes, node)
		if _, include := selectedSet[node.Node]; include {
			result.Nodes = append(result.Nodes, NodeStatus{Name: node.Node, Address: definition.Address, State: node.Phase, Runtime: runtimeState, SSHHost: "127.0.0.1", SSHPort: node.SSHPort, ProcessID: node.Process.PID})
		}
	}
	var leasePlan statusLeaseSyncPlan
	if needsLeaseSync {
		leasePlan, err = m.planStatusLeaseSyncLocked(ctx, projectValue, nodes)
		if err != nil {
			return Status{}, err
		}
	}
	for _, node := range convergenceCandidates {
		if err := store.WriteNode(node); err != nil {
			return Status{}, err
		}
	}
	if needsLeaseSync {
		if err := applyStatusLeaseSyncLocked(ctx, projectValue.Marker.ProjectID, leasePlan); err != nil {
			return Status{}, err
		}
	}
	return result, nil
}

func (m Manager) statusFor(ctx context.Context, projectValue project.Project, message string) (Status, error) {
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
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
	return m.statusFor(ctx, projectValue, "private project status")
}

func (m Manager) Connection(ctx context.Context, requestedNode string) (Connection, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Connection{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Connection{}, errors.New("current project has no valid private state")
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
		return Connection{}, fmt.Errorf("private project has no node %q", requestedNode)
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
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return "", errors.New("current project has no valid private state")
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
		return "", fmt.Errorf("private project has no node %q", nodeName)
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
	event := diagnostics.Event{Schema: 1, Time: time.Now().UTC(), Level: level, ProjectID: projectValue.Marker.ProjectID, Node: "project", OperationID: m.OperationID, Action: action, Message: message}
	if err := diagnostics.AppendEvent(ctx, filepath.Join(projectValue.Root, "events.jsonl"), event); err != nil {
		return err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil {
		return nil
	}
	store := state.Store{Project: projectValue}
	for _, definition := range projectState.Resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			continue
		}
		record := diagnostics.QEMULogRecord{Schema: 1, Time: time.Now().UTC(), Level: level, ProjectID: projectValue.Marker.ProjectID, Node: node.Node, OperationID: m.OperationID, Action: action, Message: message + "; " + execx.Display(node.Invocation.Binary, node.Invocation.Args...)}
		if err := diagnostics.AppendQEMULog(ctx, filepath.Join(filepath.Dir(node.RootDisk), "qemu.log"), record); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) Up(ctx context.Context, requested spec.Resolved) (Status, error) {
	m.report("preflight", "Checking the private-network and native QEMU capabilities")
	m.ConfiguredDataRoot = requested.DataRoot
	var err error
	requested, err = m.materializeDataRoot(requested)
	if err != nil {
		return Status{}, err
	}
	if err := validateResolved(requested); err != nil {
		return Status{}, err
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	backend, err := m.preflight(ctx, profile, requested)
	if err != nil {
		return Status{}, err
	}
	m.Progress.Report(activity.Event{Phase: "preflight", Message: "Private-network and QEMU preflight passed", Done: true})
	selected, err := selectedNodeNames(requested, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	selectedSet := nodeNameSet(selected)
	var reusableProject *project.Project
	createNodes := []string(nil)
	hadExistingState := false
	if existing, openErr := m.openProject(false); openErr == nil {
		persisted, err := (state.Store{Project: existing}).ReadProject()
		if missingPath(err) {
			reusableProject = &existing
		} else if err != nil {
			return Status{}, err
		} else {
			hadExistingState = true
			requested = materializeExistingForwardPorts(requested, persisted.Resolved)
			store := state.Store{Project: existing}
			hasState := func(name string) bool {
				_, readErr := store.ReadNode(name)
				return readErr == nil
			}
			diff := diffResolved(persisted.Resolved, requested, hasState)
			if diff.EnvelopeChanged {
				return Status{}, fmt.Errorf("%w: project-level settings changed; run farrow plan, then farrow recreate -f <config> --force", ErrRecreateRequired)
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
					m.Progress.Report(activity.Event{Phase: "project-state", Message: "All selected private nodes are already running", Done: true})
					return status, nil
				}
				return m.startExisting(ctx, existing, persisted, profile, backend)
			} else if allRunnable {
				return m.startExisting(ctx, existing, persisted, profile, backend)
			} else {
				return Status{}, errors.New("private project has mixed or transitional node phases; repair is required")
			}
		}
	} else if !missingPath(openErr) {
		return Status{}, openErr
	}
	preLease, err := m.leaseStore().Inspect()
	if err != nil {
		return Status{}, err
	}
	if !preLease.Active {
		if err := m.ensureSSHAddressesUnused(requested.Nodes); err != nil {
			return Status{}, err
		}
	}
	m.report("image-resolve", fmt.Sprintf("Resolving images for %d private node(s)", len(selected)))
	bases, boot, err := m.resolveBases(ctx, profile, requested)
	if err != nil {
		return Status{}, err
	}
	qemuPath, err := platform.FindQEMUBinary(profile, m.lookPath)
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
	projectValue := project.Project{}
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
	reserved, err := portregistry.Reserved(projectValue.DataRoot)
	if err != nil {
		return Status{}, err
	}
	resolved, sshPorts, err := materializeMissingNodePorts(requested, createNodes, reserved, state.Store{Project: projectValue})
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
	leaseStatus, err := m.leaseStore().Inspect()
	if err != nil {
		return Status{}, err
	}
	var existingLease *lease.Lease
	if leaseStatus.Active && leaseStatus.Lease.ProjectID == projectValue.Marker.ProjectID {
		existingLease = leaseStatus.Lease
	} else if hadExistingState {
		existingLease = &lease.Lease{ProjectID: projectValue.Marker.ProjectID, OwnerUID: os.Getuid()}
		createSet := nodeNameSet(createNodes)
		store := state.Store{Project: projectValue}
		for _, definition := range resolved.Nodes {
			if _, create := createSet[definition.Name]; create {
				continue
			}
			node, readErr := store.ReadNode(definition.Name)
			if readErr != nil {
				return Status{}, readErr
			}
			existingLease.Nodes = append(existingLease.Nodes, lease.Node{Name: node.Node, VMUUID: node.VMUUID})
		}
	}
	plan, err := Build(resolved, projectValue.Marker.ProjectID, os.Getuid(), existingLease, nil)
	if err != nil {
		return Status{}, err
	}
	if len(createNodes) != 0 && leaseStatus.Active && leaseStatus.Lease.ProjectID == projectValue.Marker.ProjectID && len(leaseStatus.Lease.Nodes) != len(plan.Lease.Nodes) {
		if _, err := m.leaseStore().Reshape(ctx, plan.Lease); err != nil {
			return Status{}, err
		}
	}
	generations := make(map[string]uint64, len(resolved.Nodes))
	for _, node := range resolved.Nodes {
		generations[node.Name] = 1
	}
	seeds, err := RenderSeeds(resolved, plan, SeedInput{PublicKey: publicKey, PrivateKey: string(privateKey), SpecHashes: nodeHashes, Generation: generations})
	if err != nil {
		return Status{}, err
	}
	firmware, err := platform.FindFirmwareForBoot(profile, boot)
	if err != nil {
		return Status{}, err
	}
	prepare := PrepareConfig{
		ProjectRoot: projectValue.Root, Resolved: resolved, SpecHash: specHash, NodeHashes: nodeHashes, Plan: plan, Seeds: seeds, Bases: bases, SSHPorts: sshPorts,
		Profile: profile, QEMUBinary: qemuPath, Firmware: firmware, UseUEFI: boot == "uefi", Backend: backend,
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
	controller := Controller{Project: projectValue, LeaseStore: m.leaseStore(), Prepare: prepare, Lifecycle: lifecycle, Concurrency: boundedConcurrency(len(resolved.Nodes)), ReadyTimeout: readyTimeout, NoWait: m.NoWait, CreateNodes: createNodes, StartNodes: startNodes, Version: m.FarrowVersion, Progress: m.Progress}
	createResult, err := controller.CreateAndStart(ctx)
	if err != nil {
		if m.RollbackFailed {
			err = rollbackCreateFailure(projectValue, createResult, err)
		}
		return Status{}, err
	}
	if len(createNodes) != 0 {
		projectState, err := (state.Store{Project: projectValue}).ReadProject()
		if err != nil {
			return Status{}, err
		}
		leaseStatus, err := m.leaseStore().Inspect()
		if err != nil || !leaseStatus.Active || leaseStatus.Lease.ProjectID != projectValue.Marker.ProjectID {
			return Status{}, errors.New("partial recreate completed but its private lease is absent or mismatched")
		}
		allStates := make([]state.NodeState, 0, len(projectState.Resolved.Nodes))
		store := state.Store{Project: projectValue}
		for _, definition := range projectState.Resolved.Nodes {
			node, readErr := store.ReadNode(definition.Name)
			if readErr != nil {
				return Status{}, readErr
			}
			allStates = append(allStates, node)
		}
		desiredLease, err := SynchronizeLease(*leaseStatus.Lease, allStates)
		if err != nil {
			return Status{}, err
		}
		if _, err := m.leaseStore().Update(ctx, desiredLease); err != nil {
			return Status{}, err
		}
		return m.startExisting(ctx, projectValue, projectState, profile, backend)
	}
	return m.statusFor(ctx, projectValue, "created and started private project")
}

func leaseIntentFromState(projectValue project.Project, projectState state.ProjectState) (Plan, error) {
	store := state.Store{Project: projectValue}
	existing := &lease.Lease{ProjectID: projectValue.Marker.ProjectID, OwnerUID: os.Getuid()}
	for _, definition := range projectState.Resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return Plan{}, err
		}
		existing.Nodes = append(existing.Nodes, lease.Node{Name: node.Node, VMUUID: node.VMUUID})
	}
	return Build(projectState.Resolved, projectValue.Marker.ProjectID, os.Getuid(), existing, nil)
}

func (m Manager) startExisting(ctx context.Context, projectValue project.Project, projectState state.ProjectState, profile platform.Profile, backend Backend) (Status, error) {
	m.report("preflight", "Checking the existing private project before start")
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
	preStartStore := state.Store{Project: projectValue}
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
	plan, err := leaseIntentFromState(projectValue, projectState)
	if err != nil {
		return Status{}, err
	}
	allocatorContext, cancelAllocator := context.WithTimeout(ctx, 30*time.Second)
	defer cancelAllocator()
	allocator, err := lock.Acquire(allocatorContext, filepath.Join(projectValue.DataRoot, "locks", "allocator.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer allocator.Release()
	acquired, err := m.leaseStore().Acquire(ctx, plan.Lease)
	if err != nil {
		return Status{}, err
	}
	allNodeStates := make([]state.NodeState, 0, len(projectState.Resolved.Nodes))
	for _, definition := range projectState.Resolved.Nodes {
		node, err := preStartStore.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		allNodeStates = append(allNodeStates, node)
	}
	synchronized, err := SynchronizeLease(acquired.Lease, allNodeStates)
	if err != nil {
		return Status{}, err
	}
	updatedLease, err := m.leaseStore().Update(ctx, synchronized)
	if err != nil {
		return Status{}, err
	}
	acquired.Lease = updatedLease.Lease
	lockContext, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		_, _ = m.leaseStore().Abort(ctx, acquired.Lease.ProjectID, true, reservedRuntimeAuditor)
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
	store := state.Store{Project: projectValue}
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
		m.Progress.Report(activity.Event{Phase: "project-state", Message: "All selected private nodes are already running", Done: true})
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
	outcomes, _, err := StartPrepared(ctx, StartConfig{Project: projectValue, LeaseStore: m.leaseStore(), Lifecycle: lifecycle, Nodes: names, Concurrency: boundedConcurrency(len(names)), ReadyTimeout: readyTimeout, NoWait: m.NoWait})
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
	return m.statusForLocked(ctx, projectValue, "started private project")
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
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project has no valid private state")
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
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project has no valid private state")
	}
	lockContext, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	names, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	selectedSet := nodeNameSet(names)
	releaseLease := true
	store := state.Store{Project: projectValue}
	for _, definition := range projectState.Resolved.Nodes {
		if _, selected := selectedSet[definition.Name]; selected {
			continue
		}
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		if node.Phase != state.Stopped {
			releaseLease = false
		}
	}
	lifecycle := NativeLifecycle{VM: vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: projectState.Resolved.SSHUser}}
	outcomes, _, err := StopRunning(ctx, StopConfig{Project: projectValue, LeaseStore: m.leaseStore(), Lifecycle: lifecycle, Nodes: names, Concurrency: boundedConcurrency(len(names)), ReleaseLease: releaseLease, Auditor: lease.RuntimeIdentityAuditor(m.runner(), time.Second)})
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
	message := "stopped selected private nodes; host-global lease retained for running peers"
	if releaseLease {
		message = "stopped private project and released lease"
	}
	return m.statusForLocked(ctx, projectValue, message)
}

func (m Manager) Restart(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project has no valid private state")
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
	shareBinaries, err := selectedShareInvocationBinaries(state.Store{Project: projectValue}, projectState.Resolved, selected)
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
	m.ConfiguredDataRoot = requested.DataRoot
	var err error
	requested, err = m.materializeDataRoot(requested)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if err := validateResolved(requested); err != nil {
		return LifecyclePlan{}, err
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return LifecyclePlan{}, err
	}
	if _, err := m.preflight(ctx, profile, requested); err != nil {
		return LifecyclePlan{}, err
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
	leaseStatus, err := m.leaseStore().Inspect()
	if err != nil {
		return LifecyclePlan{}, err
	}
	if !leaseStatus.Active {
		if err := m.ensureSSHAddressesUnused(requested.Nodes); err != nil {
			return LifecyclePlan{}, err
		}
	}
	result.LeaseActive = leaseStatus.Active
	projectValue, err := m.openProject(false)
	if missingPath(err) {
		return result, nil
	}
	if err != nil {
		return LifecyclePlan{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
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
	store := state.Store{Project: projectValue}
	hasState := func(name string) bool {
		_, readErr := store.ReadNode(name)
		return readErr == nil
	}
	diff := diffResolved(projectState.Resolved, requested, hasState)
	result.Create = append([]string(nil), diff.Create...)
	result.Recreate = append([]string(nil), diff.Changed...)
	result.Missing = append([]string(nil), diff.Removed...)
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

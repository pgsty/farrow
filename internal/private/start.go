package private

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/runtimepath"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

type NodeLifecycle interface {
	Start(context.Context, state.NodeState) (process.Identity, error)
	WaitReady(context.Context, state.NodeState, string, time.Duration) error
}

type NativeLifecycle struct {
	VM           vm.Lifecycle
	Project      project.Project
	Shares       map[string][]spec.Share
	SSHPath      string
	PrivateKey   string
	KnownHosts   string
	DarwinSocket string
}

func (l NativeLifecycle) PreflightStart(node state.NodeState) error {
	bundle, err := openPrivateNodeShares(l.Project, l.Shares, node)
	if err != nil {
		return err
	}
	if err := bundle.Close(); err != nil {
		return fmt.Errorf("close preflight host shares for private node %s: %w", node.Node, err)
	}
	return nil
}

func (l NativeLifecycle) privateNetworkFile() (*os.File, error) {
	if l.DarwinSocket == "" {
		return nil, errors.New("private FD invocation has no Darwin socket")
	}
	connection, err := net.DialTimeout("unix", l.DarwinSocket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial socket_vmnet FD fallback: %w", err)
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, errors.New("socket_vmnet fallback did not return a Unix connection")
	}
	file, err := unixConnection.File()
	_ = unixConnection.Close()
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (l NativeLifecycle) Start(ctx context.Context, node state.NodeState) (process.Identity, error) {
	bundle, err := openPrivateNodeShares(l.Project, l.Shares, node)
	if err != nil {
		return process.Identity{}, err
	}
	defer bundle.Close()

	shareFiles := bundle.Files()
	extraFiles := make([]*os.File, 0, len(shareFiles)+1)
	if node.Invocation.UsesPrivateFD3() {
		file, err := l.privateNetworkFile()
		if err != nil {
			return process.Identity{}, err
		}
		defer file.Close()
		extraFiles = append(extraFiles, file)
	}
	extraFiles = append(extraFiles, shareFiles...)

	var identityValue process.Identity
	if len(extraFiles) == 0 {
		identityValue, err = l.VM.Start(ctx, node.Invocation, node.Runtime.QMP, node.Runtime.PIDFile, node.Node, node.VMUUID)
	} else {
		identityValue, err = l.VM.StartWithExtraFiles(ctx, node.Invocation, node.Runtime.QMP, node.Runtime.PIDFile, node.Node, node.VMUUID, extraFiles)
	}
	if err != nil {
		return process.Identity{}, err
	}
	if len(shareFiles) == 0 {
		return identityValue, nil
	}
	if err := bundle.Recheck(); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stopErr := l.VM.Stop(cleanupContext, node.Runtime.QMP, node.Node, node.VMUUID, identityValue, node.Invocation, 5*time.Second)
		if stopErr != nil {
			return process.Identity{}, errors.Join(fmt.Errorf("recheck host shares after starting private node %s: %w", node.Node, err), fmt.Errorf("stop private node after failed host-share recheck: %w", stopErr))
		}
		return process.Identity{}, fmt.Errorf("recheck host shares after starting private node %s: %w", node.Node, err)
	}
	return identityValue, nil
}

func (l NativeLifecycle) WaitReady(ctx context.Context, node state.NodeState, projectID string, timeout time.Duration) error {
	return l.VM.WaitReady(ctx, l.SSHPath, l.PrivateKey, l.KnownHosts, node.SSHPort, vm.ReadyMarker{Project: projectID, Node: node.Node, Generation: node.Generation, SpecHash: node.SpecHash}, timeout)
}

type StartConfig struct {
	Project      project.Project
	LeaseStore   lease.Store
	Lifecycle    NodeLifecycle
	Nodes        []string
	Concurrency  int
	ReadyTimeout time.Duration
	NoWait       bool
	SetupRuntime func(string) error
	Now          func() time.Time
}

type StartOutcome struct {
	Node    string `json:"node"`
	Running bool   `json:"running"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error,omitempty"`
}

func (config StartConfig) now() time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

func setupRuntime(path string) error {
	if err := runtimepath.Ensure(path, os.Getuid()); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("private runtime directory is unsafe")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("private runtime directory is not empty; run repair before start")
	}
	return nil
}

func loadNodes(store state.Store, names []string) ([]state.NodeState, error) {
	if len(names) == 0 {
		return nil, errors.New("private start requires at least one node")
	}
	result := make([]state.NodeState, 0, len(names))
	seen := make(map[string]struct{})
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("private start repeats node %s", name)
		}
		seen[name] = struct{}{}
		node, err := store.ReadNode(name)
		if err != nil {
			return nil, err
		}
		if node.Phase != state.Prepared && node.Phase != state.Stopped {
			return nil, fmt.Errorf("private node %s phase %s is not startable", name, node.Phase)
		}
		if node.Process != (state.ProcessIdentity{}) {
			return nil, fmt.Errorf("private node %s has a recorded process identity", name)
		}
		result = append(result, node)
	}
	return result, nil
}

func writeNodes(store state.Store, nodes []state.NodeState) error {
	for _, node := range nodes {
		if err := store.WriteNode(node); err != nil {
			return err
		}
	}
	return nil
}

func synchronizeLeaseStore(ctx context.Context, store lease.Store, nodes []state.NodeState) (lease.Lease, error) {
	active, err := store.Read()
	if err != nil {
		return lease.Lease{}, err
	}
	desired, err := SynchronizeLease(active, nodes)
	if err != nil {
		return lease.Lease{}, err
	}
	updated, err := store.Update(ctx, desired)
	if err != nil {
		return lease.Lease{}, err
	}
	return updated.Lease, nil
}

func StartPrepared(ctx context.Context, config StartConfig) ([]StartOutcome, lease.Lease, error) {
	if config.Project.Root == "" || config.Lifecycle == nil {
		return nil, lease.Lease{}, errors.New("private start project or lifecycle is incomplete")
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 4
	}
	if config.Concurrency > 16 {
		config.Concurrency = 16
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 180 * time.Second
	}
	if config.SetupRuntime == nil {
		config.SetupRuntime = setupRuntime
	}
	store := state.Store{Project: config.Project}
	nodes, err := loadNodes(store, config.Nodes)
	if err != nil {
		return nil, lease.Lease{}, err
	}
	preflight, hasPreflight := config.Lifecycle.(interface {
		PreflightStart(state.NodeState) error
	})
	for _, node := range nodes {
		if hasPreflight {
			if err := preflight.PreflightStart(node); err != nil {
				return nil, lease.Lease{}, fmt.Errorf("preflight private node %s before start: %w", node.Node, err)
			}
			continue
		}
		if len(node.Invocation.ShareFiles()) != 0 {
			return nil, lease.Lease{}, fmt.Errorf("private node %s lifecycle cannot preflight host shares", node.Node)
		}
	}
	for index := range nodes {
		nodes[index].Phase = state.Starting
		nodes[index].UpdatedAt = config.now()
	}
	if err := writeNodes(store, nodes); err != nil {
		return nil, lease.Lease{}, err
	}
	startingLease, err := synchronizeLeaseStore(ctx, config.LeaseStore, nodes)
	if err != nil {
		return nil, lease.Lease{}, fmt.Errorf("mirror starting nodes into private lease: %w", err)
	}
	_ = startingLease
	outcomes := make([]StartOutcome, len(nodes))
	jobs := make(chan int)
	var wait sync.WaitGroup
	for worker := 0; worker < config.Concurrency; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				node := nodes[index]
				outcomes[index].Node = node.Node
				if err := config.SetupRuntime(node.Runtime.Directory); err != nil {
					outcomes[index].Error = err.Error()
					continue
				}
				identityValue, err := config.Lifecycle.Start(ctx, node)
				if err != nil {
					outcomes[index].Error = err.Error()
					continue
				}
				node.Process = state.ProcessIdentity{PID: identityValue.PID, Executable: identityValue.Executable, Started: identityValue.Started, ArgvHash: identityValue.ArgvHash}
				node.Phase = state.Running
				node.UpdatedAt = config.now()
				if err := store.WriteNode(node); err != nil {
					outcomes[index].Error = err.Error()
					continue
				}
				nodes[index] = node
				outcomes[index].Running = true
				if config.NoWait {
					outcomes[index].Ready = true
					continue
				}
				if err := config.Lifecycle.WaitReady(ctx, node, config.Project.Marker.ProjectID, config.ReadyTimeout); err != nil {
					outcomes[index].Error = err.Error()
					continue
				}
				outcomes[index].Ready = true
			}
		}()
	}
	for index := range nodes {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	for index := range nodes {
		persisted, readErr := store.ReadNode(nodes[index].Node)
		if readErr != nil {
			return outcomes, lease.Lease{}, readErr
		}
		nodes[index] = persisted
	}
	finalLease, err := synchronizeLeaseStore(ctx, config.LeaseStore, nodes)
	if err != nil {
		return outcomes, lease.Lease{}, fmt.Errorf("mirror final node states into private lease: %w", err)
	}
	return outcomes, finalLease, nil
}

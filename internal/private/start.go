package private

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pgsty/piglet/internal/lease"
	"github.com/pgsty/piglet/internal/process"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/runtimepath"
	"github.com/pgsty/piglet/internal/state"
	"github.com/pgsty/piglet/internal/vm"
)

type NodeLifecycle interface {
	Start(context.Context, state.NodeState) (process.Identity, error)
	WaitReady(context.Context, state.NodeState, string, time.Duration) error
}

type NativeLifecycle struct {
	VM           vm.Lifecycle
	SSHPath      string
	PrivateKey   string
	KnownHosts   string
	DarwinSocket string
}

func (l NativeLifecycle) Start(ctx context.Context, node state.NodeState) (process.Identity, error) {
	if node.Invocation.UsesPrivateFD3() {
		if l.DarwinSocket == "" {
			return process.Identity{}, errors.New("private FD invocation has no Darwin socket")
		}
		connection, err := net.DialTimeout("unix", l.DarwinSocket, 5*time.Second)
		if err != nil {
			return process.Identity{}, fmt.Errorf("dial socket_vmnet FD fallback: %w", err)
		}
		unixConnection, ok := connection.(*net.UnixConn)
		if !ok {
			_ = connection.Close()
			return process.Identity{}, errors.New("socket_vmnet fallback did not return a Unix connection")
		}
		file, err := unixConnection.File()
		_ = unixConnection.Close()
		if err != nil {
			return process.Identity{}, err
		}
		defer file.Close()
		return l.VM.StartWithExtraFiles(ctx, node.Invocation, node.Runtime.QMP, node.Runtime.PIDFile, node.Node, node.VMUUID, []*os.File{file})
	}
	return l.VM.Start(ctx, node.Invocation, node.Runtime.QMP, node.Runtime.PIDFile, node.Node, node.VMUUID)
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

func ReadyNames(outcomes []StartOutcome) []string {
	result := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Ready {
			result = append(result, outcome.Node)
		}
	}
	return result
}

func RunningNames(outcomes []StartOutcome) []string {
	result := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Running {
			result = append(result, outcome.Node)
		}
	}
	return result
}

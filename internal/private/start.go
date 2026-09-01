package private

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/runtimepath"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

type NodeLifecycle interface {
	Start(context.Context, state.NodeState) (process.Identity, error)
	WaitReady(context.Context, state.NodeState, time.Duration) error
}

type StartAborter interface {
	AbortStart(context.Context, state.NodeState, process.Identity) error
}

type NativeLifecycle struct {
	VM           vm.Lifecycle
	Deployment   Deployment
	Shares       map[string][]spec.Share
	SSHPath      string
	PrivateKey   string
	KnownHosts   string
	DarwinSocket string
}

func (l NativeLifecycle) PreflightStart(node state.NodeState) error {
	bundle, err := openPrivateNodeShares(l.Deployment, l.Shares, node)
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

func (l NativeLifecycle) Start(ctx context.Context, node state.NodeState) (_ process.Identity, returnErr error) {
	bundle, err := openPrivateNodeShares(l.Deployment, l.Shares, node)
	if err != nil {
		return process.Identity{}, err
	}
	defer func() {
		if err := bundle.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close inherited host-share files: %w", err))
		}
	}()

	shareFiles := bundle.Files()
	extraFiles := make([]*os.File, 0, len(shareFiles)+1)
	if node.Invocation.UsesPrivateFD3() {
		file, err := l.privateNetworkFile()
		if err != nil {
			return process.Identity{}, err
		}
		defer func() {
			if err := file.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close inherited private-network file: %w", err))
			}
		}()
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
	return identityValue, nil
}

func (l NativeLifecycle) AbortStart(ctx context.Context, node state.NodeState, identity process.Identity) error {
	if err := l.VM.AbortIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID, identity, node.Invocation); err != nil {
		return err
	}
	return cleanupRuntime(node)
}

func (l NativeLifecycle) WaitReady(ctx context.Context, node state.NodeState, timeout time.Duration) error {
	return l.VM.WaitReady(ctx, l.SSHPath, l.PrivateKey, l.KnownHosts, node.SSHPort, vm.ReadyMarker{Node: node.Node, Generation: node.Generation, SpecHash: node.SpecHash}, timeout)
}

type StartConfig struct {
	Deployment   Deployment
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

type readyConfig struct {
	Deployment   Deployment
	Lifecycle    NodeLifecycle
	Nodes        []string
	Concurrency  int
	ReadyTimeout time.Duration
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
		return errors.New("private runtime directory is not empty; run farrow status to converge it, then retry")
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

func StartPrepared(ctx context.Context, config StartConfig) ([]StartOutcome, error) {
	if config.Deployment.Root == "" || config.Lifecycle == nil {
		return nil, errors.New("private start deployment or lifecycle is incomplete")
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
	store := state.Store{Root: config.Deployment.Root}
	nodes, err := loadNodes(store, config.Nodes)
	if err != nil {
		return nil, err
	}
	preflight, hasPreflight := config.Lifecycle.(interface {
		PreflightStart(state.NodeState) error
	})
	for _, node := range nodes {
		if hasPreflight {
			if err := preflight.PreflightStart(node); err != nil {
				return nil, fmt.Errorf("preflight private node %s before start: %w", node.Node, err)
			}
			continue
		}
		if len(node.Invocation.ShareFiles()) != 0 {
			return nil, fmt.Errorf("private node %s lifecycle cannot preflight host shares", node.Node)
		}
	}
	for index := range nodes {
		nodes[index].Phase = state.Starting
		nodes[index].UpdatedAt = config.now()
	}
	if err := writeNodes(store, nodes); err != nil {
		return nil, err
	}
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
					message := fmt.Sprintf("persist running state for %s: %v", node.Node, err)
					if aborter, ok := config.Lifecycle.(StartAborter); ok {
						if abortErr := aborter.AbortStart(ctx, node, identityValue); abortErr != nil {
							message += "; compensation failed: " + abortErr.Error() + "; the QEMU process may still be running; run farrow status"
						} else {
							message += "; compensation stopped QEMU and removed its runtime files"
						}
					} else {
						message += "; lifecycle cannot compensate the started process; run farrow status"
					}
					outcomes[index].Error = message
					continue
				}
				nodes[index] = node
				outcomes[index].Running = true
				if config.NoWait {
					outcomes[index].Ready = true
					continue
				}
				if err := config.Lifecycle.WaitReady(ctx, node, config.ReadyTimeout); err != nil {
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
	return outcomes, nil
}

// waitRunningReady rechecks the guest contract without restarting an already
// running QEMU process. This lets a later `up` converge a prior partial run.
func waitRunningReady(ctx context.Context, config readyConfig) ([]StartOutcome, error) {
	if config.Deployment.Root == "" || config.Lifecycle == nil {
		return nil, errors.New("private readiness deployment or lifecycle is incomplete")
	}
	if len(config.Nodes) == 0 {
		return nil, errors.New("private readiness requires at least one node")
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
	store := state.Store{Root: config.Deployment.Root}
	nodes := make([]state.NodeState, 0, len(config.Nodes))
	seen := make(map[string]struct{}, len(config.Nodes))
	for _, name := range config.Nodes {
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("private readiness repeats node %s", name)
		}
		seen[name] = struct{}{}
		node, err := store.ReadNode(name)
		if err != nil {
			return nil, err
		}
		if node.Phase != state.Running || node.Process.PID <= 0 {
			return nil, fmt.Errorf("private node %s is not running for readiness", name)
		}
		nodes = append(nodes, node)
	}
	outcomes := make([]StartOutcome, len(nodes))
	jobs := make(chan int)
	var wait sync.WaitGroup
	for worker := 0; worker < config.Concurrency; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				node := nodes[index]
				outcomes[index] = StartOutcome{Node: node.Node, Running: true}
				if err := config.Lifecycle.WaitReady(ctx, node, config.ReadyTimeout); err != nil {
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
	return outcomes, nil
}

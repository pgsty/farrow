package private

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

type fakeStopLifecycle struct {
	mu        sync.Mutex
	active    int
	maxActive int
	fail      map[string]bool
	timeouts  []time.Duration
}

func (fake *fakeStopLifecycle) Stop(_ context.Context, node state.NodeState, timeout time.Duration) error {
	fake.mu.Lock()
	fake.active++
	fake.timeouts = append(fake.timeouts, timeout)
	if fake.active > fake.maxActive {
		fake.maxActive = fake.active
	}
	fake.mu.Unlock()
	defer func() {
		fake.mu.Lock()
		fake.active--
		fake.mu.Unlock()
	}()
	time.Sleep(10 * time.Millisecond)
	if fake.fail[node.Node] {
		return errors.New("injected stop failure")
	}
	return nil
}

func runningStopFixture(t *testing.T) StartConfig {
	t.Helper()
	config, _ := preparedStartFixture(t)
	config.Lifecycle = &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	if _, _, err := StartPrepared(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	return config
}

func deadAuditor(_ context.Context, node lease.Node) (lease.Observation, error) {
	return lease.Observation{Node: node.Name, Authority: "dead", Evidence: "fake process/QMP are dead"}, nil
}

func TestStopRunningParallelAndReleaseLease(t *testing.T) {
	startConfig := runningStopFixture(t)
	fake := &fakeStopLifecycle{fail: map[string]bool{}}
	outcomes, active, err := StopRunning(context.Background(), StopConfig{
		Project: startConfig.Project, LeaseStore: startConfig.LeaseStore, Lifecycle: fake,
		Nodes: startConfig.Nodes, Concurrency: 2, CleanupRuntime: func(state.NodeState) error { return nil },
		ReleaseLease: true, Auditor: deadAuditor,
	})
	if err != nil || len(StoppedNames(outcomes)) != 2 || active != nil {
		t.Fatalf("stop outcomes=%#v active=%#v err=%v", outcomes, active, err)
	}
	if fake.maxActive < 2 {
		t.Fatalf("nodes did not stop concurrently: max=%d", fake.maxActive)
	}
	for _, timeout := range fake.timeouts {
		if timeout != vm.GracefulGuestShutdownTimeout {
			t.Fatalf("default guest shutdown timeout=%s, want %s", timeout, vm.GracefulGuestShutdownTimeout)
		}
	}
	if _, err := startConfig.LeaseStore.Read(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("all-stopped lease remains: %v", err)
	}
	store := state.Store{Project: startConfig.Project}
	for _, name := range startConfig.Nodes {
		node, err := store.ReadNode(name)
		if err != nil || node.Phase != state.Stopped || node.Process != (state.ProcessIdentity{}) {
			t.Fatalf("stopped node %s = %#v, %v", name, node, err)
		}
	}
	second, active, err := StopRunning(context.Background(), StopConfig{
		Project: startConfig.Project, LeaseStore: startConfig.LeaseStore, Lifecycle: fake,
		Nodes: startConfig.Nodes, Concurrency: 2, CleanupRuntime: func(state.NodeState) error { return nil },
		ReleaseLease: true, Auditor: deadAuditor,
	})
	if err != nil || active != nil || len(StoppedNames(second)) != 2 {
		t.Fatalf("idempotent stop outcomes=%#v active=%#v err=%v", second, active, err)
	}
}

func TestStopRunningPreservesPartialFailureAndLease(t *testing.T) {
	startConfig := runningStopFixture(t)
	fake := &fakeStopLifecycle{fail: map[string]bool{"node-1": true}}
	outcomes, active, err := StopRunning(context.Background(), StopConfig{
		Project: startConfig.Project, LeaseStore: startConfig.LeaseStore, Lifecycle: fake,
		Nodes: startConfig.Nodes, Concurrency: 2, CleanupRuntime: func(state.NodeState) error { return nil },
		ReleaseLease: true, Auditor: deadAuditor,
	})
	if err != nil || len(StoppedNames(outcomes)) != 1 || outcomes[1].Error == "" || active == nil {
		t.Fatalf("partial stop outcomes=%#v active=%#v err=%v", outcomes, active, err)
	}
	for _, node := range active.Nodes {
		if node.Name == "meta" && node.Phase != lease.Stopped {
			t.Fatalf("successful stop not mirrored: %#v", node)
		}
		if node.Name == "node-1" && node.Phase != lease.Stopping {
			t.Fatalf("failed stop not retained: %#v", node)
		}
	}
	if _, err := startConfig.LeaseStore.Read(); err != nil {
		t.Fatalf("partial-stop lease was released: %v", err)
	}
}

func TestStopRunningSelectedNodeKeepsPeerAndLease(t *testing.T) {
	startConfig := runningStopFixture(t)
	fake := &fakeStopLifecycle{fail: map[string]bool{}}
	outcomes, active, err := StopRunning(context.Background(), StopConfig{
		Project: startConfig.Project, LeaseStore: startConfig.LeaseStore, Lifecycle: fake,
		Nodes: []string{"meta"}, Concurrency: 1, CleanupRuntime: func(state.NodeState) error { return nil },
		ReleaseLease: false, Auditor: deadAuditor,
	})
	if err != nil || len(outcomes) != 1 || !outcomes[0].Stopped || active == nil {
		t.Fatalf("selected stop outcomes=%#v active=%#v err=%v", outcomes, active, err)
	}
	store := state.Store{Project: startConfig.Project}
	meta, metaErr := store.ReadNode("meta")
	peer, peerErr := store.ReadNode("node-1")
	if metaErr != nil || peerErr != nil || meta.Phase != state.Stopped || peer.Phase != state.Running {
		t.Fatalf("selected stop phases meta=%#v/%v peer=%#v/%v", meta, metaErr, peer, peerErr)
	}
	if _, err := startConfig.LeaseStore.Read(); err != nil {
		t.Fatalf("selected stop released active peer lease: %v", err)
	}
}

func TestStopRunningAcceptsAlreadyStoppedPeerAndReleasesLease(t *testing.T) {
	startConfig := runningStopFixture(t)
	store := state.Store{Project: startConfig.Project}
	peer, err := store.ReadNode("node-1")
	if err != nil {
		t.Fatal(err)
	}
	peer.Phase = state.Stopped
	peer.Process = state.ProcessIdentity{}
	peer.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(peer); err != nil {
		t.Fatal(err)
	}
	active, err := startConfig.LeaseStore.Read()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.ReadNode("meta")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := SynchronizeLease(active, []state.NodeState{meta, peer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := startConfig.LeaseStore.Update(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStopLifecycle{fail: map[string]bool{"node-1": true}}
	outcomes, remaining, err := StopRunning(context.Background(), StopConfig{
		Project: startConfig.Project, LeaseStore: startConfig.LeaseStore, Lifecycle: fake,
		Nodes: startConfig.Nodes, Concurrency: 2, CleanupRuntime: func(state.NodeState) error { return nil },
		ReleaseLease: true, Auditor: deadAuditor,
	})
	if err != nil || remaining != nil || len(StoppedNames(outcomes)) != 2 || outcomes[1].Error != "" {
		t.Fatalf("mixed stop outcomes=%#v remaining=%#v err=%v", outcomes, remaining, err)
	}
	if _, err := startConfig.LeaseStore.Read(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mixed stop lease remains: %v", err)
	}
}

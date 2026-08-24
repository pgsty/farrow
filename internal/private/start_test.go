package private

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/lease"
	"github.com/pgsty/piglet/internal/process"
	"github.com/pgsty/piglet/internal/state"
)

type fakeNodeLifecycle struct {
	mu        sync.Mutex
	active    int
	maxActive int
	failStart map[string]bool
	failReady map[string]bool
	waitCalls int
}

func (fake *fakeNodeLifecycle) Start(_ context.Context, node state.NodeState) (process.Identity, error) {
	fake.mu.Lock()
	fake.active++
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
	if fake.failStart[node.Node] {
		return process.Identity{}, errors.New("injected QEMU start failure")
	}
	pid := 100
	if node.Node != "meta" {
		pid++
	}
	return process.Identity{PID: pid, Executable: node.Invocation.Binary, Started: "test-start", ArgvHash: "test-argv-hash"}, nil
}

func (fake *fakeNodeLifecycle) WaitReady(_ context.Context, node state.NodeState, _ string, _ time.Duration) error {
	fake.mu.Lock()
	fake.waitCalls++
	fake.mu.Unlock()
	if fake.failReady[node.Node] {
		return errors.New("injected readiness failure")
	}
	return nil
}

func TestStartPreparedNoWaitStopsAtVerifiedProcessStart(t *testing.T) {
	config, _ := preparedStartFixture(t)
	config.NoWait = true
	fake := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{"meta": true, "node-1": true}}
	config.Lifecycle = fake
	outcomes, active, err := StartPrepared(context.Background(), config)
	if err != nil || len(ReadyNames(outcomes)) != 2 || len(RunningNames(outcomes)) != 2 {
		t.Fatalf("no-wait outcomes=%#v lease=%#v err=%v", outcomes, active, err)
	}
	fake.mu.Lock()
	waitCalls := fake.waitCalls
	fake.mu.Unlock()
	if waitCalls != 0 {
		t.Fatalf("no-wait invoked guest readiness %d time(s)", waitCalls)
	}
}

func preparedStartFixture(t *testing.T) (StartConfig, []state.NodeState) {
	t.Helper()
	projectValue, prepareConfig, outcomes := commitFixture(t, &fakePrivateDisks{})
	committed, err := CommitPrepared(context.Background(), projectValue, prepareConfig, outcomes, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	leaseStore := lease.Store{Root: testLeaseRoot(t), OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1}
	acquired, err := leaseStore.Acquire(context.Background(), prepareConfig.Plan.Lease)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := SynchronizeLease(acquired.Lease, committed.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := leaseStore.Update(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range committed.Nodes {
		if err := FinalizePrepared(projectValue, node.Node, LeaseNodeVerifier(updated.Lease, lease.Prepared)); err != nil {
			t.Fatal(err)
		}
	}
	return StartConfig{
		Project: projectValue, LeaseStore: leaseStore, Nodes: []string{"meta", "node-1"},
		Concurrency: 2, ReadyTimeout: time.Second, SetupRuntime: func(string) error { return nil },
	}, committed.Nodes
}

func TestStartPreparedParallelSuccessAndLeasePhases(t *testing.T) {
	config, _ := preparedStartFixture(t)
	fake := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	config.Lifecycle = fake
	outcomes, active, err := StartPrepared(context.Background(), config)
	if err != nil || len(ReadyNames(outcomes)) != 2 || len(RunningNames(outcomes)) != 2 || active.Generation != 4 {
		t.Fatalf("start outcomes=%#v lease=%#v err=%v", outcomes, active, err)
	}
	if fake.maxActive < 2 {
		t.Fatalf("nodes did not start concurrently: max=%d", fake.maxActive)
	}
	store := state.Store{Project: config.Project}
	for _, name := range config.Nodes {
		node, err := store.ReadNode(name)
		if err != nil || node.Phase != state.Running || node.Process.PID == 0 {
			t.Fatalf("running state %s = %#v, %v", name, node, err)
		}
	}
	for _, node := range active.Nodes {
		if node.Phase != lease.Running || node.Process.PID == 0 {
			t.Fatalf("running lease node = %#v", node)
		}
	}
}

func TestStartPreparedPreservesRunningPeerOnReadinessFailure(t *testing.T) {
	config, _ := preparedStartFixture(t)
	fake := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{"node-1": true}}
	config.Lifecycle = fake
	outcomes, active, err := StartPrepared(context.Background(), config)
	if err != nil || len(RunningNames(outcomes)) != 2 || len(ReadyNames(outcomes)) != 1 || outcomes[1].Error == "" {
		t.Fatalf("partial readiness outcomes=%#v lease=%#v err=%v", outcomes, active, err)
	}
	for _, node := range active.Nodes {
		if node.Phase != lease.Running {
			t.Fatalf("readiness failure incorrectly stopped peer/node: %#v", node)
		}
	}
}

func TestStartPreparedLeavesFailedStartForRepair(t *testing.T) {
	config, _ := preparedStartFixture(t)
	fake := &fakeNodeLifecycle{failStart: map[string]bool{"node-1": true}, failReady: map[string]bool{}}
	config.Lifecycle = fake
	outcomes, active, err := StartPrepared(context.Background(), config)
	if err != nil || len(RunningNames(outcomes)) != 1 || outcomes[1].Error == "" {
		t.Fatalf("partial start outcomes=%#v lease=%#v err=%v", outcomes, active, err)
	}
	for _, node := range active.Nodes {
		if node.Name == "meta" && node.Phase != lease.Running {
			t.Fatalf("successful peer not running: %#v", node)
		}
		if node.Name == "node-1" && node.Phase != lease.Starting {
			t.Fatalf("failed start not left for repair: %#v", node)
		}
	}
}

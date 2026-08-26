package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

type rejectingStatusRunner struct{}

func (rejectingStatusRunner) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{}, errors.New("injected process identity rejection")
}

func persistRuntimeStates(t *testing.T, config StartConfig, nodes []state.NodeState) []state.NodeState {
	t.Helper()
	store := state.Store{Project: config.Project}
	for _, node := range nodes {
		if err := store.WriteNode(node); err != nil {
			t.Fatal(err)
		}
	}
	active, err := config.LeaseStore.Read()
	if err != nil {
		t.Fatal(err)
	}
	desired, err := SynchronizeLease(active, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.LeaseStore.Update(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	return nodes
}

func deadRunningStates(nodes []state.NodeState) []state.NodeState {
	result := append([]state.NodeState(nil), nodes...)
	for index := range result {
		result[index].Phase = state.Running
		result[index].Process = state.ProcessIdentity{
			PID: 1<<30 + index, Executable: result[index].Invocation.Binary,
			Started: "stale-start", ArgvHash: "stale-argv-hash",
		}
		result[index].UpdatedAt = time.Now().UTC()
	}
	return result
}

func TestPrivateStatusConvergesSelfHaltedNodesAndReleasesLease(t *testing.T) {
	startConfig, nodes := preparedStartFixture(t)
	nodes = deadRunningStates(nodes)
	for _, node := range nodes {
		if err := setupRuntime(node.Runtime.Directory); err != nil {
			t.Fatal(err)
		}
		runtimeDirectory := node.Runtime.Directory
		t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })
		if err := os.WriteFile(node.Runtime.PIDFile, []byte(fmt.Sprintf("%d\n", node.Process.PID)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	persistRuntimeStates(t, startConfig, nodes)

	manager := Manager{CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	status, err := manager.Status(context.Background())
	if err != nil || len(status.Nodes) != len(nodes) {
		t.Fatalf("self-halt status=%#v err=%v", status, err)
	}
	store := state.Store{Project: startConfig.Project}
	for _, reported := range status.Nodes {
		if reported.State != state.Stopped || reported.Runtime != "inactive" || reported.ProcessID != 0 {
			t.Fatalf("self-halted node did not converge: %#v", reported)
		}
		persisted, readErr := store.ReadNode(reported.Name)
		if readErr != nil || persisted.Phase != state.Stopped || persisted.Process != (state.ProcessIdentity{}) {
			t.Fatalf("converged node state=%#v err=%v", persisted, readErr)
		}
		if _, statErr := os.Lstat(persisted.Runtime.PIDFile); statErr != nil {
			t.Fatalf("status removed stale runtime evidence %s: %v", persisted.Runtime.PIDFile, statErr)
		}
	}
	leaseStatus, err := startConfig.LeaseStore.Inspect()
	if err != nil || leaseStatus.Active {
		t.Fatalf("all-dead private lease remains active: %#v err=%v", leaseStatus, err)
	}
}

func TestPrivateStatusAuditsAllNodesBeforeConverging(t *testing.T) {
	startConfig, nodes := preparedStartFixture(t)
	nodes = deadRunningStates(nodes)
	deadIdentity := nodes[0].Process
	liveIndex := len(nodes) - 1
	nodes[liveIndex].Process = state.ProcessIdentity{
		PID: os.Getpid(), Executable: nodes[liveIndex].Invocation.Binary,
		Started: "not-the-current-process", ArgvHash: "not-the-current-process",
	}
	persistRuntimeStates(t, startConfig, nodes)

	manager := Manager{
		CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore,
		Runner: rejectingStatusRunner{},
	}
	_, err := manager.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "alive but full process identity does not match") {
		t.Fatalf("live mismatched PID was not rejected: %v", err)
	}
	store := state.Store{Project: startConfig.Project}
	deadPersisted, readErr := store.ReadNode(nodes[0].Node)
	if readErr != nil || deadPersisted.Phase != state.Running || deadPersisted.Process != deadIdentity {
		t.Fatalf("status partially converged an earlier dead node before rejecting a later mismatch: %#v err=%v", deadPersisted, readErr)
	}
	livePersisted, readErr := store.ReadNode(nodes[liveIndex].Node)
	if readErr != nil || livePersisted.Phase != state.Running || livePersisted.Process.PID != os.Getpid() {
		t.Fatalf("fail-closed status mutated live mismatched state: %#v err=%v", livePersisted, readErr)
	}
	leaseStatus, inspectErr := startConfig.LeaseStore.Inspect()
	if inspectErr != nil || !leaseStatus.Active {
		t.Fatalf("fail-closed status released the lease: %#v err=%v", leaseStatus, inspectErr)
	}
}

func TestPrivateStatusPreauditsLeaseBeforeWritingConvergence(t *testing.T) {
	startConfig, nodes := preparedStartFixture(t)
	nodes = deadRunningStates(nodes)
	persistRuntimeStates(t, startConfig, nodes)

	active, err := startConfig.LeaseStore.Read()
	if err != nil {
		t.Fatal(err)
	}
	for index := range active.Nodes {
		if active.Nodes[index].Name == nodes[0].Node {
			active.Nodes[index].Process = process.Identity{
				PID: os.Getpid(), Executable: nodes[0].Invocation.Binary,
				Started: "not-the-current-process", ArgvHash: "not-the-current-process",
			}
		}
	}
	if _, err := startConfig.LeaseStore.Update(context.Background(), active); err != nil {
		t.Fatal(err)
	}

	manager := Manager{
		CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore,
		Runner: rejectingStatusRunner{},
	}
	if _, err := manager.Status(context.Background()); err == nil {
		t.Fatal("status accepted a lease whose recorded runtime could not be proved dead")
	}
	store := state.Store{Project: startConfig.Project}
	for _, original := range nodes {
		persisted, readErr := store.ReadNode(original.Node)
		if readErr != nil || persisted.Phase != state.Running || persisted.Process != original.Process {
			t.Fatalf("lease preaudit rejection partially mutated %s: %#v err=%v", original.Node, persisted, readErr)
		}
	}
	leaseStatus, inspectErr := startConfig.LeaseStore.Inspect()
	if inspectErr != nil || !leaseStatus.Active {
		t.Fatalf("lease preaudit rejection released the lease: %#v err=%v", leaseStatus, inspectErr)
	}
}

func TestPrivateUpContinuesAfterSelfHaltConvergenceInSameCall(t *testing.T) {
	startConfig, nodes := preparedStartFixture(t)
	store := state.Store{Project: startConfig.Project}
	projectState, err := store.ReadProject()
	if err != nil {
		t.Fatal(err)
	}
	projectState.Resolved.DataRoot = startConfig.Project.DataRoot
	projectState.SpecHash, err = spec.Hash(projectState.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	projectState.UpdatedAt = time.Now().UTC()
	if err := store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	nodes = deadRunningStates(nodes)
	for index := range nodes {
		nodes[index].SpecHash = projectState.SpecHash
	}
	persistRuntimeStates(t, startConfig, nodes)

	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	secondPreflight := errors.New("second preflight proves Up continued into startExisting")
	hostCalls := 0
	manager := Manager{
		CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore,
		NativeProfile: func() (platform.Profile, error) { return profile, nil },
		NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
			return netpreflight.Report{Ready: true}
		},
		HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
			hostCalls++
			if hostCalls == 2 {
				return Backend{}, secondPreflight
			}
			return Backend{}, nil
		},
	}
	_, err = manager.Up(context.Background(), projectState.Resolved)
	if !errors.Is(err, secondPreflight) || hostCalls != 2 {
		t.Fatalf("Up did not continue after same-call convergence: calls=%d err=%v", hostCalls, err)
	}
	for _, node := range nodes {
		persisted, readErr := store.ReadNode(node.Node)
		if readErr != nil || persisted.Phase != state.Stopped || persisted.Process != (state.ProcessIdentity{}) {
			t.Fatalf("same-call convergence state=%#v err=%v", persisted, readErr)
		}
	}
	leaseStatus, err := startConfig.LeaseStore.Inspect()
	if err != nil || leaseStatus.Active {
		t.Fatalf("converged lease was not released before restart attempt: %#v err=%v", leaseStatus, err)
	}
	for _, node := range nodes {
		if _, err := os.Lstat(node.Runtime.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Up convergence unexpectedly created or cleaned runtime directory %s: %v", node.Runtime.Directory, err)
		}
	}
}

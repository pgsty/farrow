package private

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/state"
)

func TestPrivateRepairConvergesDeadRunningStateAndReleasesLease(t *testing.T) {
	runtimeParent := "/tmp"
	if runtime.GOOS == "darwin" {
		runtimeParent = "/private/tmp"
	}
	runtimeRoot, err := os.MkdirTemp(runtimeParent, "pr.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeRoot)
	startConfig, nodes := preparedStartFixture(t)
	store := state.Store{Project: startConfig.Project}
	for index := range nodes {
		nodes[index].Phase = state.Running
		nodes[index].Process = state.ProcessIdentity{PID: 999990 + index, Executable: nodes[index].Invocation.Binary, Started: "stale", ArgvHash: "stale"}
		nodes[index].UpdatedAt = time.Now().UTC()
		if err := store.WriteNode(nodes[index]); err != nil {
			t.Fatal(err)
		}
		if err := setupRuntime(nodes[index].Runtime.Directory); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(nodes[index].Runtime.PIDFile, []byte("999999\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	active, err := startConfig.LeaseStore.Read()
	if err != nil {
		t.Fatal(err)
	}
	desired, err := SynchronizeLease(active, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := startConfig.LeaseStore.Update(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	manager := Manager{CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore, Nodes: []string{"meta"}}
	dryRun, err := manager.Repair(context.Background(), false)
	if err != nil || len(dryRun.Actions) < 3 {
		t.Fatalf("private repair dry run = %#v, %v", dryRun, err)
	}
	for _, node := range nodes {
		persisted, _ := store.ReadNode(node.Node)
		if persisted.Phase != state.Running {
			t.Fatal("dry-run changed private node state")
		}
	}
	selected, err := manager.Repair(context.Background(), true)
	if err != nil || selected.Blocked {
		t.Fatalf("selected private repair apply = %#v, %v", selected, err)
	}
	meta, metaErr := store.ReadNode("meta")
	peer, peerErr := store.ReadNode("node-1")
	if metaErr != nil || peerErr != nil || meta.Phase != state.Stopped || peer.Phase != state.Running {
		t.Fatalf("selected repair touched wrong nodes: meta=%#v/%v peer=%#v/%v", meta, metaErr, peer, peerErr)
	}
	status, err := startConfig.LeaseStore.Inspect()
	if err != nil || !status.Active {
		t.Fatalf("selected repair released peer lease: %#v, %v", status, err)
	}
	manager.Nodes = nil
	applied, err := manager.Repair(context.Background(), true)
	if err != nil || applied.Blocked {
		t.Fatalf("full private repair apply = %#v, %v", applied, err)
	}
	for _, node := range nodes {
		persisted, err := store.ReadNode(node.Node)
		if err != nil || persisted.Phase != state.Stopped || persisted.Process != (state.ProcessIdentity{}) {
			t.Fatalf("repaired node = %#v, %v", persisted, err)
		}
		if _, err := os.Lstat(node.Runtime.Directory); !os.IsNotExist(err) {
			t.Fatalf("stale runtime remains: %s: %v", node.Runtime.Directory, err)
		}
	}
	status, err = startConfig.LeaseStore.Inspect()
	if err != nil || status.Active {
		t.Fatalf("dead repaired lease remains: %#v, %v", status, err)
	}
}

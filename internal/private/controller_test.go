package private

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/state"
)

func controllerFixture(t *testing.T, disks DiskOps, lifecycle NodeLifecycle) Controller {
	t.Helper()
	root := t.TempDir()
	projectValue := Deployment{Root: root, DataRoot: root}
	prepare := privatePrepareConfig(t, projectValue.Root, disks)
	var err error
	prepare.Plan, err = Build(prepare.Resolved, identity.DeploymentID, os.Getuid(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey := testSSHKeyPair(t)
	prepare.Seeds, err = RenderSeeds(prepare.Resolved, prepare.Plan, SeedInput{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		SpecHashes: prepare.NodeHashes, Generation: map[string]uint64{"meta": 1, "node-1": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Controller{
		Project: projectValue, LeaseStore: lease.Store{Root: testLeaseRoot(t), OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1},
		Prepare: prepare, Lifecycle: lifecycle, Concurrency: 2, ReadyTimeout: time.Second,
		SetupRuntime: func(string) error { return nil }, Version: "test-version",
	}
}

func TestControllerCreateAndStartFullSuccess(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{}, lifecycle)
	result, err := controller.CreateAndStart(context.Background())
	if err != nil || len(result.Commit.Nodes) != 2 || len(readyNames(result.Start)) != 2 {
		t.Fatalf("controller result=%#v err=%v", result, err)
	}
	for _, node := range result.Lease.Nodes {
		if node.Phase != lease.Running {
			t.Fatalf("controller lease node not running: %#v", node)
		}
	}
}

func TestControllerCreatesAllButStartsSelectedNode(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{}, lifecycle)
	controller.StartNodes = []string{"node-1"}
	result, err := controller.CreateAndStart(context.Background())
	if err != nil || len(result.Commit.Nodes) != 2 || len(result.Start) != 1 || result.Start[0].Node != "node-1" || !result.Start[0].Ready {
		t.Fatalf("selected controller result=%#v err=%v", result, err)
	}
	store := state.Store{Root: controller.Project.Root}
	meta, metaErr := store.ReadNode("meta")
	worker, workerErr := store.ReadNode("node-1")
	if metaErr != nil || workerErr != nil || meta.Phase != state.Prepared || worker.Phase != state.Running {
		t.Fatalf("selected phases meta=%#v/%v worker=%#v/%v", meta, metaErr, worker, workerErr)
	}
	for _, node := range result.Lease.Nodes {
		if node.Name == "meta" && node.Phase != lease.Prepared {
			t.Fatalf("unselected lease node=%#v", node)
		}
		if node.Name == "node-1" && node.Phase != lease.Running {
			t.Fatalf("selected lease node=%#v", node)
		}
	}
}

func TestControllerCreateAndStartReturnsTypedPartialAndKeepsSuccess(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{failSubstring: "node-1/root.qcow2"}, lifecycle)
	result, err := controller.CreateAndStart(context.Background())
	operationErr := err
	var partial *PartialError
	if !errors.As(err, &partial) || len(partial.Nodes) != 1 || partial.Nodes[0] != "node-1" || len(readyNames(result.Start)) != 1 || readyNames(result.Start)[0] != "meta" {
		t.Fatalf("partial controller result=%#v partial=%#v err=%v", result, partial, err)
	}
	persisted, err := (state.Store{Root: controller.Project.Root}).ReadNode("meta")
	if err != nil || persisted.Phase != state.Running {
		t.Fatalf("successful node not preserved running: %#v %v", persisted, err)
	}
	if _, err := (state.Store{Root: controller.Project.Root}).ReadNode("node-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed node gained stable state: %v", err)
	}
	dryRun, err := RollbackFailedPrepares(controller.Project, result, false)
	if err != nil || len(dryRun) != 1 || dryRun[0].Node != "node-1" {
		t.Fatalf("failed rollback dry run = %#v, %v", dryRun, err)
	}
	annotated := rollbackCreateFailure(controller.Project, result, operationErr)
	if !errors.As(annotated, &partial) || len(partial.RolledBack) != 1 || partial.RolledBack[0] != "node-1" {
		t.Fatalf("failed rollback annotation = %#v, %v", partial, annotated)
	}
	if _, err := os.Lstat(filepath.Join(controller.Project.Root, "nodes", "meta", "state.json")); err != nil {
		t.Fatalf("failed-node rollback touched successful node: %v", err)
	}
}

func TestControllerManagerStartSelectionSkipsPrepareFailure(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{failSubstring: "node-1/root.qcow2"}, lifecycle)
	// Manager.Up supplies the selected create nodes explicitly, including a node
	// that may subsequently fail PrepareSelected.
	controller.StartNodes = []string{"meta", "node-1"}

	result, err := controller.CreateAndStart(context.Background())
	var partial *PartialError
	if !errors.As(err, &partial) || len(partial.Nodes) != 1 || partial.Nodes[0] != "node-1" {
		t.Fatalf("manager-style start selection result=%#v partial=%#v err=%v", result, partial, err)
	}
	if ready := readyNames(result.Start); len(ready) != 1 || ready[0] != "meta" {
		t.Fatalf("successful prepared peer did not start: result=%#v ready=%v", result, ready)
	}
	meta, metaErr := (state.Store{Root: controller.Project.Root}).ReadNode("meta")
	if metaErr != nil || meta.Phase != state.Running {
		t.Fatalf("successful prepared peer state=%#v err=%v", meta, metaErr)
	}
	if _, nodeErr := (state.Store{Root: controller.Project.Root}).ReadNode("node-1"); !errors.Is(nodeErr, os.ErrNotExist) {
		t.Fatalf("failed prepare gained committed state: %v", nodeErr)
	}
}

func TestControllerFailedSelectedStartDoesNotStartUnselectedPeer(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{failSubstring: "node-1/root.qcow2"}, lifecycle)
	controller.StartNodes = []string{"node-1"}

	result, err := controller.CreateAndStart(context.Background())
	var partial *PartialError
	if !errors.As(err, &partial) || len(partial.Nodes) != 1 || partial.Nodes[0] != "node-1" || len(result.Start) != 0 {
		t.Fatalf("failed selected start result=%#v partial=%#v err=%v", result, partial, err)
	}
	meta, metaErr := (state.Store{Root: controller.Project.Root}).ReadNode("meta")
	if metaErr != nil || meta.Phase != state.Prepared {
		t.Fatalf("unselected successful peer state=%#v err=%v", meta, metaErr)
	}
}

func TestControllerTotalPrepareFailureReleasesPreStartLease(t *testing.T) {
	t.Parallel()
	controller := controllerFixture(t, &fakePrivateDisks{failSubstring: "root.qcow2"}, &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}})
	_, err := controller.CreateAndStart(context.Background())
	var partial *PartialError
	if !errors.As(err, &partial) || len(partial.Nodes) != 2 {
		t.Fatalf("total prepare failure = %#v, %v", partial, err)
	}
	status, inspectErr := controller.LeaseStore.Inspect()
	if inspectErr != nil || !status.Available || status.Active {
		t.Fatalf("pre-start lease was not released: %#v, inspect=%v operation=%v", status, inspectErr, err)
	}
}

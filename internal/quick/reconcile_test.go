package quick

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/cloudinit"
	"github.com/pgsty/piglet/internal/disk"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

type reconcileRunner struct {
	sizes map[string]int64
}

func (runner *reconcileRunner) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	if len(args) == 0 {
		return execx.Result{}, errors.New("missing fake qemu-img operation")
	}
	switch args[0] {
	case "info":
		pathname := args[len(args)-1]
		size, ok := runner.sizes[pathname]
		if !ok {
			return execx.Result{}, fmt.Errorf("unknown fake disk %s", pathname)
		}
		info := disk.Info{Filename: pathname, Format: "qcow2", VirtualSize: size}
		if filepath.Base(pathname) == "root.qcow2" {
			info.BackingFilename = "/managed/base.qcow2"
			info.FullBackingFilename = "/managed/base.qcow2"
			info.BackingFilenameFormat = "qcow2"
		}
		data, _ := json.Marshal(info)
		return execx.Result{Stdout: data}, nil
	case "resize":
		pathname := args[len(args)-2]
		target, err := strconv.ParseInt(args[len(args)-1], 10, 64)
		if err != nil {
			return execx.Result{}, err
		}
		runner.sizes[pathname] = target
		return execx.Result{}, nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected fake operation %q", args[0])
	}
}

type reconcileFixture struct {
	manager      Manager
	store        state.Store
	projectState state.ProjectState
	node         state.NodeState
	runner       *reconcileRunner
}

func newReconcileFixture(t *testing.T) reconcileFixture {
	t.Helper()
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(workDir, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	nodeDir, err := projectValue.EnsureNodeDir(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	keyDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAATEST reconcile-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved := spec.Quick(true, true)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	projectState := state.ProjectState{
		Schema: state.ProjectSchema, PigletVersion: "test", ProjectID: projectValue.Marker.ProjectID,
		SpecHash: hash, Resolved: resolved, UpdatedAt: now,
	}
	store := state.Store{Project: projectValue}
	if err := store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	rootDisk := filepath.Join(nodeDir, "root.qcow2")
	dataDisk := filepath.Join(nodeDir, "data.qcow2")
	nvram := filepath.Join(nodeDir, "nvram.fd")
	for _, pathname := range []string{rootDisk, dataDisk, nvram} {
		if err := os.WriteFile(pathname, []byte("fake-runtime-artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vmUUID, err := project.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	dataState, err := desiredDataState(projectValue, resolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := newRuntimeDirectory(projectValue.Marker.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	node := state.NodeState{
		Schema: state.NodeSchema, PigletVersion: "test", ProjectID: projectValue.Marker.ProjectID,
		Node: nodeName, VMUUID: vmUUID, Phase: state.Stopped, Generation: 1, SpecHash: hash,
		Image:    state.Image{Alias: "u24", Release: "test", Digest: "digest", VirtualSize: 1},
		RootDisk: rootDisk, DataDisks: dataState, Seed: filepath.Join(nodeDir, "seed.iso"), NVRAM: nvram,
		SSHPort: 2222, Forwards: qemuForwards(resolved, 2222),
		Runtime:   state.RuntimePaths{Directory: runtimeDir, QMP: filepath.Join(runtimeDir, "qmp.sock"), PIDFile: filepath.Join(runtimeDir, "qemu.pid")},
		CreatedAt: now, UpdatedAt: now,
	}
	node.Invocation, err = buildInvocation(projectValue, projectState, node)
	if err != nil {
		t.Fatal(err)
	}
	seedFiles, err := reconcileSeedFiles(projectValue, projectState, node)
	if err != nil {
		t.Fatal(err)
	}
	if err := cloudinit.BuildISO(node.Seed, seedFiles); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	operationID, err := project.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	runner := &reconcileRunner{sizes: map[string]int64{rootDisk: resolved.Nodes[0].RootDisk, dataDisk: resolved.Nodes[0].Disks[0].Size}}
	return reconcileFixture{
		manager: Manager{CWD: workDir, PigletVersion: "test-next", OperationID: operationID, QEMUImg: "/fake/qemu-img", Runner: runner},
		store:   store, projectState: projectState, node: node, runner: runner,
	}
}

func TestReconcileAppliesOfflineRootAndDataGrowth(t *testing.T) {
	fixture := newReconcileFixture(t)
	desired := fixture.projectState.Resolved
	desired.Nodes[0].CPUs++
	desired.Nodes[0].RootDisk += spec.GiB
	desired.Nodes[0].Disks[0].Size += spec.GiB

	committed, report, err := fixture.manager.beginReconcile(context.Background(), fixture.store, fixture.projectState, fixture.node, desired, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if committed.Generation != fixture.node.Generation+1 || committed.Phase != state.Stopped || committed.SpecHash == fixture.node.SpecHash || len(report.Actions) < 5 {
		t.Fatalf("committed reconcile = %#v report=%#v", committed, report)
	}
	if fixture.runner.sizes[committed.RootDisk] != desired.Nodes[0].RootDisk || fixture.runner.sizes[committed.DataDisks[0].Path] != desired.Nodes[0].Disks[0].Size {
		t.Fatalf("fake disk sizes = %#v", fixture.runner.sizes)
	}
	projectState, err := fixture.store.ReadProject()
	if err != nil || projectState.SpecHash != committed.SpecHash || projectState.Resolved.Nodes[0].CPUs != desired.Nodes[0].CPUs {
		t.Fatalf("committed project = %#v, %v", projectState, err)
	}
	if !seedMatches(committed.Seed, &state.ReconcileIntent{Project: projectState, Node: committed}) {
		t.Fatal("committed seed does not match desired generation/hash")
	}
	if _, err := fixture.store.ReadTransaction(nodeName); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reconcile transaction remains: %v", err)
	}
}

func TestRepairCompletesPartiallyCommittedReconcile(t *testing.T) {
	fixture := newReconcileFixture(t)
	desired := fixture.projectState.Resolved
	desired.Nodes[0].Memory += spec.GiB
	desired.Nodes[0].Disks[0].Size += spec.GiB
	transaction, err := fixture.manager.stageReconcile(fixture.store, fixture.projectState, fixture.node, desired, "stop")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after one irreversible disk change and one of the two
	// state files committed, but before seed publication/node state commit.
	fixture.runner.sizes[fixture.node.DataDisks[0].Path] = desired.Nodes[0].Disks[0].Size
	if err := fixture.store.WriteProject(transaction.Reconcile.Project); err != nil {
		t.Fatal(err)
	}

	dryRun, err := fixture.manager.Repair(context.Background(), false)
	if err != nil || len(dryRun.Actions) < 3 {
		t.Fatalf("dry-run repair = %#v, %v", dryRun, err)
	}
	if _, err := os.Lstat(transaction.Reconcile.StagedSeed); err != nil {
		t.Fatalf("dry run removed staged seed: %v", err)
	}
	stillOld, err := fixture.store.ReadNode(nodeName)
	if err != nil || stillOld.Generation != fixture.node.Generation {
		t.Fatalf("dry run changed node: %#v %v", stillOld, err)
	}

	applied, err := fixture.manager.Repair(context.Background(), true)
	if err != nil || applied.Blocked {
		t.Fatalf("applied repair = %#v, %v", applied, err)
	}
	committed, err := fixture.store.ReadNode(nodeName)
	if err != nil || committed.Generation != fixture.node.Generation+1 || committed.SpecHash != transaction.Reconcile.Project.SpecHash {
		t.Fatalf("recovered node = %#v, %v", committed, err)
	}
	if _, err := fixture.store.ReadTransaction(nodeName); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered transaction remains: %v", err)
	}
}

func TestLifecycleRefusesPendingReconcileTransaction(t *testing.T) {
	fixture := newReconcileFixture(t)
	desired := fixture.projectState.Resolved
	desired.Nodes[0].CPUs++
	transaction, err := fixture.manager.stageReconcile(fixture.store, fixture.projectState, fixture.node, desired, "restart")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("start did not refuse pending reconcile: %v", err)
	}
	if _, err := os.Lstat(transaction.Reconcile.StagedSeed); err != nil {
		t.Fatalf("refused lifecycle mutated staged seed: %v", err)
	}
	if _, err := fixture.store.ReadTransaction(nodeName); err != nil {
		t.Fatalf("refused lifecycle mutated journal: %v", err)
	}
}

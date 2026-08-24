package quick

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/piglet/internal/disk"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/persistent"
	"github.com/pgsty/piglet/internal/spec"
)

type destroyRunner struct{}

func (destroyRunner) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{}, nil
}

func persistentQuickFixture(t *testing.T) reconcileFixture {
	t.Helper()
	fixture := newReconcileFixture(t)
	projectState := fixture.projectState
	projectState.Resolved.Nodes[0].Disks[0].Persistent = true
	hash, err := spec.Hash(projectState.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	projectState.SpecHash = hash
	node := fixture.node
	node.SpecHash = hash
	node.DataDisks[0].Persistent = true
	node.Invocation, err = buildInvocation(fixture.store.Project, projectState, node)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	keysDir := filepath.Join(fixture.store.Project.Root, "keys")
	if err := os.WriteFile(filepath.Join(keysDir, "known_hosts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.manager.Runner = destroyRunner{}
	fixture.projectState = projectState
	fixture.node = node
	return fixture
}

func TestQuickDestroyPreservesAndReattachesPersistentDisk(t *testing.T) {
	t.Parallel()
	fixture := persistentQuickFixture(t)
	original, err := os.ReadFile(fixture.node.DataDisks[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err := persistent.Inventory(fixture.store.Project)
	if err != nil || len(records) != 1 {
		t.Fatalf("persistent inventory = %#v, %v", records, err)
	}
	if data, err := os.ReadFile(records[0].Path); err != nil || string(data) != string(original) {
		t.Fatalf("persistent data changed: %q, %v", data, err)
	}
	nodeDir, err := fixture.store.Project.EnsureNodeDir(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	runner := &reconcileRunner{sizes: map[string]int64{records[0].Path: fixture.projectState.Resolved.Nodes[0].Disks[0].Size}}
	manager := disk.Manager{QEMUImg: "/fake/qemu-img", Runner: runner}
	path, serial, err := resolveQuickDataDisk(context.Background(), fixture.store.Project, fixture.projectState.Resolved, manager, nodeDir, fixture.projectState.Resolved.Nodes[0].Disks[0])
	if err != nil {
		t.Fatal(err)
	}
	if path != records[0].Path || serial != records[0].Serial {
		t.Fatalf("reattached path/serial = %s/%s, record=%#v", path, serial, records[0])
	}
}

func TestQuickPersistentReattachRejectsIncompatibleSizeAndMount(t *testing.T) {
	t.Parallel()
	fixture := persistentQuickFixture(t)
	if _, err := fixture.manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodeDir, err := fixture.store.Project.EnsureNodeDir(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*spec.Disk){
		func(value *spec.Disk) { value.Size++ },
		func(value *spec.Disk) { value.Mount = "/different" },
	} {
		resolved := fixture.projectState.Resolved
		mutate(&resolved.Nodes[0].Disks[0])
		manager := disk.Manager{QEMUImg: "/fake/qemu-img", Runner: &reconcileRunner{sizes: map[string]int64{}}}
		if _, _, err := resolveQuickDataDisk(context.Background(), fixture.store.Project, resolved, manager, nodeDir, resolved.Nodes[0].Disks[0]); err == nil {
			t.Fatalf("incompatible retained disk was accepted: %#v", resolved.Nodes[0].Disks[0])
		}
	}
}

func TestQuickDeletePersistentRequiresDestroyAndExplicitAPI(t *testing.T) {
	t.Parallel()
	fixture := persistentQuickFixture(t)
	if _, err := fixture.manager.DeletePersistent(context.Background()); err == nil {
		t.Fatal("persistent deletion was allowed before destroy")
	}
	if _, err := fixture.manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	deleted, err := fixture.manager.DeletePersistent(context.Background())
	if err != nil || len(deleted) != 1 {
		t.Fatalf("explicit persistent deletion = %#v, %v", deleted, err)
	}
	if records, err := fixture.manager.PersistentDisks(); err != nil || len(records) != 0 {
		t.Fatalf("persistent inventory after explicit delete = %#v, %v", records, err)
	}
}

func TestValidateNodePathsRejectsExternalPersistentDiskWithoutMarker(t *testing.T) {
	t.Parallel()
	fixture := persistentQuickFixture(t)
	external := filepath.Join(fixture.store.Project.Root, "unowned.qcow2")
	if err := os.WriteFile(external, []byte("unowned"), 0o600); err != nil {
		t.Fatal(err)
	}
	node := fixture.node
	node.DataDisks[0].Path = external
	if err := validateNodePaths(fixture.store.Project, node); err == nil {
		t.Fatal("unowned external persistent disk path was accepted")
	}
}

func TestQuickDestroyFailsClosedOnUnexpectedNodeArtifact(t *testing.T) {
	t.Parallel()
	fixture := persistentQuickFixture(t)
	unexpected := filepath.Join(filepath.Dir(fixture.node.RootDisk), "manual.qcow2")
	if err := os.WriteFile(unexpected, []byte("manual"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Destroy(context.Background()); err == nil {
		t.Fatal("quick destroy accepted an unexpected node artifact")
	}
	for _, path := range []string{unexpected, fixture.node.RootDisk, fixture.node.DataDisks[0].Path} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("fail-closed destroy changed %s: %v", path, err)
		}
	}
}

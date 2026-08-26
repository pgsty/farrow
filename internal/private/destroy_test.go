package private

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/persistent"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func TestPrivateDestroyRemovesOnlyNodesAndPreservesKeysProjectCache(t *testing.T) {
	t.Parallel()
	startConfig, nodes := preparedStartFixture(t)
	projectValue := startConfig.Project
	deadAudit := func(_ context.Context, node lease.Node) (lease.Observation, error) {
		return lease.Observation{Node: node.Name, Authority: "dead", Evidence: "test runtime absent"}, nil
	}
	if _, err := startConfig.LeaseStore.Abort(context.Background(), projectValue.Marker.ProjectID, true, deadAudit); err != nil {
		t.Fatal(err)
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"known_hosts": 0o600, "id_ed25519": 0o600, "id_ed25519.pub": 0o644} {
		if err := os.WriteFile(filepath.Join(keysDir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	manager := Manager{CWD: projectValue.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	result, err := manager.Destroy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != len(nodes) {
		t.Fatalf("destroy result = %#v", result)
	}
	for _, node := range nodes {
		directory, _ := projectValue.NodeDir(node.Node)
		if _, err := os.Lstat(directory); !os.IsNotExist(err) {
			t.Fatalf("destroyed node directory remains: %s: %v", directory, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(projectValue.Root, "resolved.json")); !os.IsNotExist(err) {
		t.Fatalf("resolved state remains after private destroy: %v", err)
	}
	for _, path := range []string{projectValue.MarkerPath, filepath.Join(projectValue.Root, "project.json"), keysDir, filepath.Join(keysDir, "id_ed25519"), filepath.Join(projectValue.Root, "base.qcow2")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("preserved private artifact missing %s: %v", path, err)
		}
	}
}

func TestPrivatePartialRecreateDestroyPreservesPeerStateAndLease(t *testing.T) {
	t.Parallel()
	startConfig, _ := preparedStartFixture(t)
	projectValue := startConfig.Project
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"known_hosts": 0o600, "id_ed25519": 0o600, "id_ed25519.pub": 0o644} {
		if err := os.WriteFile(filepath.Join(keysDir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	manager := Manager{CWD: projectValue.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore, Nodes: []string{"node-1"}, allowPartialDestroy: true}
	result, err := manager.Destroy(context.Background())
	if err != nil || len(result.Nodes) != 1 || result.Nodes[0].Name != "node-1" || result.Nodes[0].State != state.Absent {
		t.Fatalf("partial destroy result=%#v err=%v", result, err)
	}
	store := state.Store{Project: projectValue}
	if _, err := store.ReadNode("meta"); err != nil {
		t.Fatalf("peer state was removed: %v", err)
	}
	if _, err := store.ReadNode("node-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected node state remains: %v", err)
	}
	if _, err := store.ReadProject(); err != nil {
		t.Fatalf("project state was removed: %v", err)
	}
	leaseStatus, err := startConfig.LeaseStore.Inspect()
	if err != nil || !leaseStatus.Active {
		t.Fatalf("peer lease was released: %#v err=%v", leaseStatus, err)
	}
	for _, node := range leaseStatus.Lease.Nodes {
		if node.Name == "node-1" && node.Phase != lease.Stopped {
			t.Fatalf("selected lease node phase=%s", node.Phase)
		}
		if node.Name == "meta" && node.Phase != lease.Prepared {
			t.Fatalf("peer lease node phase=%s", node.Phase)
		}
	}
}

func TestControllerRecreatesOnlyMissingSelectedNode(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{}, lifecycle)
	created, err := controller.CreateAndStart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := state.Store{Project: controller.Project}
	states := make([]state.NodeState, 0, len(created.Commit.Nodes))
	for _, node := range created.Commit.Nodes {
		node.Phase = state.Prepared
		node.Process = state.ProcessIdentity{}
		if err := store.WriteNode(node); err != nil {
			t.Fatal(err)
		}
		states = append(states, node)
	}
	active, err := controller.LeaseStore.Read()
	if err != nil {
		t.Fatal(err)
	}
	desired, err := SynchronizeLease(active, states)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.LeaseStore.Update(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	keysDir := filepath.Join(controller.Project.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"known_hosts": 0o600, "id_ed25519": 0o600, "id_ed25519.pub": 0o644} {
		if err := os.WriteFile(filepath.Join(keysDir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	destroyer := Manager{CWD: controller.Project.WorkDir, FarrowVersion: "test", LeaseStore: &controller.LeaseStore, Nodes: []string{"node-1"}, allowPartialDestroy: true}
	if _, err := destroyer.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	controller.CreateNodes = []string{"node-1"}
	controller.StartNodes = []string{"node-1"}
	recreated, err := controller.CreateAndStart(context.Background())
	if err != nil || len(recreated.Prepare) != 1 || recreated.Prepare[0].Node != "node-1" || len(recreated.Start) != 1 || !recreated.Start[0].Ready {
		t.Fatalf("partial recreate=%#v err=%v", recreated, err)
	}
	meta, metaErr := store.ReadNode("meta")
	peer, peerErr := store.ReadNode("node-1")
	if metaErr != nil || peerErr != nil || meta.Phase != state.Prepared || peer.Phase != state.Running {
		t.Fatalf("partial recreate states meta=%#v/%v peer=%#v/%v", meta, metaErr, peer, peerErr)
	}
}

func markFixtureDiskPersistent(t *testing.T, projectValue state.Store) (state.ProjectState, state.NodeState) {
	t.Helper()
	projectState, err := projectValue.ReadProject()
	if err != nil {
		t.Fatal(err)
	}
	projectState.Resolved.Nodes[0].Disks[0].Persistent = true
	hash, err := spec.Hash(projectState.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	projectState.SpecHash = hash
	if err := projectValue.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	node, err := projectValue.ReadNode(projectState.Resolved.Nodes[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	node.SpecHash = hash
	node.DataDisks[0].Persistent = true
	if err := projectValue.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	return projectState, node
}

func TestPrivateDestroyPreservesAndPrepareReattachesPersistentDisk(t *testing.T) {
	t.Parallel()
	startConfig, _ := preparedStartFixture(t)
	projectValue := startConfig.Project
	store := state.Store{Project: projectValue}
	projectState, originalNode := markFixtureDiskPersistent(t, store)
	originalData, err := os.ReadFile(originalNode.DataDisks[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	deadAudit := func(_ context.Context, node lease.Node) (lease.Observation, error) {
		return lease.Observation{Node: node.Name, Authority: "dead", Evidence: "test runtime absent"}, nil
	}
	if _, err := startConfig.LeaseStore.Abort(context.Background(), projectValue.Marker.ProjectID, true, deadAudit); err != nil {
		t.Fatal(err)
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"known_hosts": 0o600, "id_ed25519": 0o600, "id_ed25519.pub": 0o644} {
		if err := os.WriteFile(filepath.Join(keysDir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	manager := Manager{CWD: projectValue.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	if _, err := manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err := persistent.Inventory(projectValue)
	if err != nil || len(records) != 1 {
		t.Fatalf("retained records = %#v, %v", records, err)
	}
	if data, err := os.ReadFile(records[0].Path); err != nil || string(data) != string(originalData) {
		t.Fatalf("retained data changed: %q, %v", data, err)
	}

	prepare := privatePrepareConfig(t, projectValue.Root, &fakePrivateDisks{})
	prepare.Resolved = projectState.Resolved
	prepare.SpecHash = projectState.SpecHash
	prepare.Plan, err = Build(prepare.Resolved, projectValue.Marker.ProjectID, os.Getuid(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey := testSSHKeyPair(t)
	prepare.Seeds, err = RenderSeeds(prepare.Resolved, prepare.Plan, SeedInput{PublicKey: publicKey, PrivateKey: privateKey, SpecHash: prepare.SpecHash, Generation: map[string]uint64{"meta": 1, "node-1": 1}})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := PrepareNode(context.Background(), prepare, originalNode.Node)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Data) != 1 || artifacts.Data[0].Path != records[0].Path {
		t.Fatalf("persistent disk was not reattached: %#v", artifacts.Data)
	}
}

func TestPrivatePersistentDeleteRequiresDestroyedNodes(t *testing.T) {
	t.Parallel()
	startConfig, _ := preparedStartFixture(t)
	projectValue := startConfig.Project
	_, _ = markFixtureDiskPersistent(t, state.Store{Project: projectValue})
	manager := Manager{CWD: projectValue.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	if _, err := manager.DeletePersistent(context.Background()); err == nil {
		t.Fatal("persistent disks were deleted while nodes existed")
	}
	deadAudit := func(_ context.Context, node lease.Node) (lease.Observation, error) {
		return lease.Observation{Node: node.Name, Authority: "dead", Evidence: "test runtime absent"}, nil
	}
	if _, err := startConfig.LeaseStore.Abort(context.Background(), projectValue.Marker.ProjectID, true, deadAudit); err != nil {
		t.Fatal(err)
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"known_hosts": 0o600, "id_ed25519": 0o600, "id_ed25519.pub": 0o644} {
		if err := os.WriteFile(filepath.Join(keysDir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	deleted, err := manager.DeletePersistent(context.Background())
	if err != nil || len(deleted) != 1 {
		t.Fatalf("explicit persistent deletion = %#v, %v", deleted, err)
	}
}

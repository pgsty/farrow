package private

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/farrow/internal/persistent"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func writeDestroyKeyFixtures(t *testing.T, root string) string {
	t.Helper()
	keysDir := filepath.Join(root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"known_hosts": 0o600, "id_ed25519": 0o600, "id_ed25519.pub": 0o644} {
		if err := os.WriteFile(filepath.Join(keysDir, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	return keysDir
}

func TestPrivateDestroyRemovesOnlyNodesAndPreservesKeysStateCache(t *testing.T) {
	startConfig, nodes := preparedStartFixture(t)
	projectValue := startConfig.Project
	t.Setenv("FARROW_HOME", projectValue.Root)
	keysDir := writeDestroyKeyFixtures(t, projectValue.Root)
	manager := Manager{FarrowVersion: "test"}
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
	for _, path := range []string{keysDir, filepath.Join(keysDir, "id_ed25519"), filepath.Join(projectValue.Root, "base.qcow2")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("preserved private artifact missing %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(projectValue.Root, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("full destroy must remove the deployment state document: %v", err)
	}
}

func TestPrivatePartialRecreateDestroyPreservesPeerState(t *testing.T) {
	startConfig, _ := preparedStartFixture(t)
	projectValue := startConfig.Project
	t.Setenv("FARROW_HOME", projectValue.Root)
	writeDestroyKeyFixtures(t, projectValue.Root)
	manager := Manager{FarrowVersion: "test", Nodes: []string{"node-1"}, allowPartialDestroy: true}
	result, err := manager.Destroy(context.Background())
	if err != nil || len(result.Nodes) != 1 || result.Nodes[0].Name != "node-1" || result.Nodes[0].State != state.Absent {
		t.Fatalf("partial destroy result=%#v err=%v", result, err)
	}
	store := state.Store{Root: projectValue.Root}
	if _, err := store.ReadNode("meta"); err != nil {
		t.Fatalf("peer state was removed: %v", err)
	}
	if _, err := store.ReadNode("node-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected node state remains: %v", err)
	}
	if _, err := store.ReadDeployment(); err != nil {
		t.Fatalf("deployment state was removed: %v", err)
	}
}

func TestControllerRecreatesOnlyMissingSelectedNode(t *testing.T) {
	lifecycle := &fakeNodeLifecycle{failStart: map[string]bool{}, failReady: map[string]bool{}}
	controller := controllerFixture(t, &fakePrivateDisks{}, lifecycle)
	t.Setenv("FARROW_HOME", controller.Project.Root)
	created, err := controller.CreateAndStart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := state.Store{Root: controller.Project.Root}
	for _, node := range created.Commit.Nodes {
		node.Phase = state.Prepared
		node.Process = state.ProcessIdentity{}
		if err := store.WriteNode(node); err != nil {
			t.Fatal(err)
		}
	}
	writeDestroyKeyFixtures(t, controller.Project.Root)
	destroyer := Manager{FarrowVersion: "test", Nodes: []string{"node-1"}, allowPartialDestroy: true}
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

func markFixtureDiskPersistent(t *testing.T, projectValue state.Store) (state.DeploymentState, state.NodeState) {
	t.Helper()
	projectState, err := projectValue.ReadDeployment()
	if err != nil {
		t.Fatal(err)
	}
	projectState.Resolved.Nodes[0].Disks[0].Persistent = true
	hash, err := spec.Hash(projectState.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	projectState.SpecHash = hash
	if err := projectValue.WriteDeployment(projectState); err != nil {
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
	startConfig, _ := preparedStartFixture(t)
	projectValue := startConfig.Project
	t.Setenv("FARROW_HOME", projectValue.Root)
	store := state.Store{Root: projectValue.Root}
	projectState, originalNode := markFixtureDiskPersistent(t, store)
	originalData, err := os.ReadFile(originalNode.DataDisks[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	writeDestroyKeyFixtures(t, projectValue.Root)
	manager := Manager{FarrowVersion: "test"}
	if _, err := manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, err := persistent.Inventory(projectValue.Root)
	if err != nil || len(records) != 1 {
		t.Fatalf("retained records = %#v, %v", records, err)
	}
	if data, err := os.ReadFile(records[0].Path); err != nil || string(data) != string(originalData) {
		t.Fatalf("retained data changed: %q, %v", data, err)
	}

	prepare := privatePrepareConfig(t, projectValue.Root, &fakePrivateDisks{})
	prepare.Resolved = projectState.Resolved
	prepare.SpecHash = projectState.SpecHash
	prepare.Plan, err = Build(prepare.Resolved, os.Getuid(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey := testSSHKeyPair(t)
	prepare.Seeds, err = RenderSeeds(prepare.Resolved, prepare.Plan, SeedInput{PublicKey: publicKey, PrivateKey: privateKey, SpecHashes: prepare.NodeHashes, Generation: map[string]uint64{"meta": 1, "node-1": 1}})
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
	startConfig, _ := preparedStartFixture(t)
	projectValue := startConfig.Project
	t.Setenv("FARROW_HOME", projectValue.Root)
	_, _ = markFixtureDiskPersistent(t, state.Store{Root: projectValue.Root})
	manager := Manager{FarrowVersion: "test"}
	if _, err := manager.DeletePersistent(context.Background()); err == nil {
		t.Fatal("persistent disks were deleted while nodes existed")
	}
	writeDestroyKeyFixtures(t, projectValue.Root)
	if _, err := manager.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	deleted, err := manager.DeletePersistent(context.Background())
	if err != nil || len(deleted) != 1 {
		t.Fatalf("explicit persistent deletion = %#v, %v", deleted, err)
	}
}

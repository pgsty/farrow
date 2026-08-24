package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/qemu"
	"github.com/pgsty/piglet/internal/spec"
)

func testStore(t *testing.T) Store {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	created, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	return Store{Project: created}
}

func TestProjectAndNodeRoundTrip(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	projectState := ProjectState{Schema: 1, PigletVersion: "dev", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: now}
	if err := store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	gotProject, err := store.ReadProject()
	if err != nil || gotProject.SpecHash != hash {
		t.Fatalf("project round trip: %#v %v", gotProject, err)
	}
	node := NodeState{
		Schema: 1, PigletVersion: "dev", ProjectID: store.Project.Marker.ProjectID,
		Node: "meta", VMUUID: "018f4b8e-1234-4abc-9def-0123456789ab", Phase: Prepared,
		Generation: 1, SpecHash: hash,
		Image:    Image{Alias: "u24", Release: "20260801", Digest: "abc", VirtualSize: 1},
		RootDisk: filepath.Join(store.Project.Root, "nodes", "meta", "root.qcow2"),
		Seed:     filepath.Join(store.Project.Root, "nodes", "meta", "seed.iso"), SSHPort: 2222,
		Forwards:   []qemu.Forward{{Bind: "127.0.0.1", Host: 2222, Guest: 22}},
		Runtime:    RuntimePaths{Directory: "/tmp/piglet", QMP: "/tmp/piglet/qmp.sock", PIDFile: "/tmp/piglet/qemu.pid"},
		Invocation: qemu.Invocation{Binary: "/opt/qemu", Args: []string{"-S"}},
		CreatedAt:  now, UpdatedAt: now,
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	gotNode, err := store.ReadNode("meta")
	if err != nil || gotNode.VMUUID != node.VMUUID || gotNode.Invocation.Binary != node.Invocation.Binary {
		t.Fatalf("node round trip: %#v %v", gotNode, err)
	}
}

func TestStrictStateAndHashValidation(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, _ := spec.Hash(resolved)
	value := ProjectState{Schema: 1, PigletVersion: "dev", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
	if err := store.WriteProject(value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Project.Root, "resolved.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-2] = ','
	data = append(data[:len(data)-1], []byte(`"unknown":true}`)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadProject(); err == nil {
		t.Fatal("unknown or malformed state unexpectedly accepted")
	}

	value.SpecHash = "wrong"
	if err := store.WriteProject(value); err == nil {
		t.Fatal("incorrect spec hash unexpectedly written")
	}
	value.SpecHash = hash
	value.PigletVersion = ""
	if err := store.WriteProject(value); err == nil {
		t.Fatal("empty state writer version unexpectedly accepted")
	}
}

func TestTransactionRoundTrip(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	now := time.Now().UTC()
	transaction := Transaction{Schema: 1, PigletVersion: "dev", OperationID: "op-1", ProjectID: store.Project.Marker.ProjectID, Node: "meta", From: Absent, To: Preparing, Completed: []Action{{Name: "reserve-port", Resource: "2222"}}, StartedAt: now, UpdatedAt: now}
	if err := store.WriteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadTransaction("meta")
	if err != nil || got.OperationID != transaction.OperationID || len(got.Completed) != 1 {
		t.Fatalf("transaction round trip: %#v %v", got, err)
	}
}

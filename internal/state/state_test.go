package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
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
	projectState := ProjectState{Schema: 1, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: now}
	if err := store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	gotProject, err := store.ReadProject()
	if err != nil || gotProject.SpecHash != hash {
		t.Fatalf("project round trip: %#v %v", gotProject, err)
	}
	node := NodeState{
		Schema: 1, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID,
		Node: "meta", VMUUID: "018f4b8e-1234-4abc-9def-0123456789ab", Phase: Prepared,
		Generation: 1, SpecHash: hash,
		Image:    Image{Alias: "u24", Release: "20260801", Digest: "abc", VirtualSize: 1},
		RootDisk: filepath.Join(store.Project.Root, "nodes", "meta", "root.qcow2"),
		DataDisks: []DataDisk{{
			Name: "data", Path: filepath.Join(store.Project.Root, "nodes", "meta", "data.qcow2"), Serial: "abcdefghijklmnopqrst",
			Size: 64 * spec.GiB, Mount: "/data", RequestedFilesystem: "xfs", ActualFilesystem: "xfs",
		}},
		Seed: filepath.Join(store.Project.Root, "nodes", "meta", "seed.iso"), SSHPort: 2222,
		Forwards:   []qemu.Forward{{Bind: "127.0.0.1", Host: 2222, Guest: 22}},
		Runtime:    RuntimePaths{Directory: "/tmp/farrow", QMP: "/tmp/farrow/qmp.sock", PIDFile: "/tmp/farrow/qemu.pid"},
		Invocation: qemu.Invocation{Binary: "/opt/qemu", Args: []string{"-S"}},
		CreatedAt:  now, UpdatedAt: now,
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	gotNode, err := store.ReadNode("meta")
	if err != nil || gotNode.VMUUID != node.VMUUID || gotNode.Invocation.Binary != node.Invocation.Binary || len(gotNode.DataDisks) != 1 || gotNode.DataDisks[0].RequestedFilesystem != "xfs" || gotNode.DataDisks[0].ActualFilesystem != "xfs" {
		t.Fatalf("node round trip: %#v %v", gotNode, err)
	}
}

func TestDataDiskFilesystemFieldsAreBackwardCompatible(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	node := NodeState{
		Schema: NodeSchema, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID,
		Node: "meta", VMUUID: "018f4b8e-1234-4abc-9def-0123456789ab", Phase: Prepared,
		Generation: 1, SpecHash: hash, Image: Image{Alias: "u24", Release: "test", Digest: "abc", VirtualSize: 1},
		RootDisk: filepath.Join(store.Project.Root, "nodes", "meta", "root.qcow2"),
		DataDisks: []DataDisk{{
			Name: "data", Path: filepath.Join(store.Project.Root, "nodes", "meta", "data.qcow2"), Serial: "abcdefghijklmnopqrst",
			Size: 64 * spec.GiB, Mount: "/data",
		}},
		Seed: filepath.Join(store.Project.Root, "nodes", "meta", "seed.iso"), SSHPort: 2222,
		Forwards:   []qemu.Forward{{Bind: "127.0.0.1", Host: 2222, Guest: 22}},
		Runtime:    RuntimePaths{Directory: "/tmp/farrow", QMP: "/tmp/farrow/qmp.sock", PIDFile: "/tmp/farrow/qemu.pid"},
		Invocation: qemu.Invocation{Binary: "/opt/qemu", Args: []string{"-S"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Project.Root, "nodes", "meta", "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "requested_filesystem") || strings.Contains(string(data), "actual_filesystem") {
		t.Fatalf("empty optional filesystem fields were serialized: %s", data)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["data_disks"]; !ok {
		t.Fatal("legacy-compatible node fixture lost data_disks")
	}
	got, err := store.ReadNode("meta")
	if err != nil || len(got.DataDisks) != 1 || got.DataDisks[0].RequestedFilesystem != "" || got.DataDisks[0].ActualFilesystem != "" {
		t.Fatalf("legacy filesystem fields did not decode as unknown: %#v, %v", got.DataDisks, err)
	}
}

func TestDataDiskFilesystemStatePreservesContradictoryEvidence(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, _ := spec.Hash(resolved)
	now := time.Now().UTC()
	node := NodeState{
		Schema: NodeSchema, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID,
		Node: "meta", VMUUID: "018f4b8e-1234-4abc-9def-0123456789ab", Phase: Prepared,
		Generation: 1, SpecHash: hash, Image: Image{Alias: "u24"}, RootDisk: "/root.qcow2", Seed: "/seed.iso", SSHPort: 2222,
		DataDisks: []DataDisk{{Name: "data", Path: "/data.qcow2", Serial: "abcdefghijklmnopqrst", Size: spec.GiB, Mount: "/data", RequestedFilesystem: "ext4", ActualFilesystem: "xfs"}},
		Forwards:  []qemu.Forward{}, Runtime: RuntimePaths{Directory: "/tmp/farrow", QMP: "/tmp/farrow/qmp.sock", PIDFile: "/tmp/farrow/qemu.pid"},
		Invocation: qemu.Invocation{Binary: "/opt/qemu", Args: []string{"-S"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadNode("meta")
	if err != nil || got.DataDisks[0].RequestedFilesystem != "ext4" || got.DataDisks[0].ActualFilesystem != "xfs" {
		t.Fatalf("contradictory filesystem evidence was not preserved: %#v, %v", got.DataDisks, err)
	}
}

func TestProjectForwardRequestEvidenceAndLegacyCompatibility(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	resolved.Nodes[0].Forwards[0] = spec.WithMaterializedHost(resolved.Nodes[0].Forwards[0], 25432)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	value := ProjectState{Schema: ProjectSchema, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
	if err := store.WriteProject(value); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store.Project.Root, "resolved.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"requested_host": 15432`) {
		t.Fatalf("project state lost requested host evidence: %s", data)
	}
	got, err := store.ReadProject()
	if err != nil || got.Resolved.Nodes[0].Forwards[0].RequestedHost != 15432 {
		t.Fatalf("requested host evidence did not round trip: %#v, %v", got.Resolved.Nodes[0].Forwards[0], err)
	}

	legacy := resolved
	legacy.Nodes = append([]spec.Node(nil), resolved.Nodes...)
	legacy.Nodes[0].Forwards = append([]spec.Forward(nil), resolved.Nodes[0].Forwards...)
	legacy.Nodes[0].Forwards[0].RequestedHost = 0
	legacyHash, err := spec.Hash(legacy)
	if err != nil {
		t.Fatal(err)
	}
	value.Resolved, value.SpecHash = legacy, legacyHash
	if err := store.WriteProject(value); err != nil {
		t.Fatalf("legacy state without requested_host was rejected: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(store.Project.Root, "resolved.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "requested_host") {
		t.Fatalf("legacy optional field was synthesized without evidence: %s", data)
	}

	invalid := resolved
	invalid.Nodes = append([]spec.Node(nil), resolved.Nodes...)
	invalid.Nodes[0].Forwards = append([]spec.Forward(nil), resolved.Nodes[0].Forwards...)
	invalid.Nodes[0].Forwards[0].RequestedHost = invalid.Nodes[0].Forwards[0].Host
	invalidHash, err := spec.Hash(invalid)
	if err != nil {
		t.Fatal(err)
	}
	value.Resolved, value.SpecHash = invalid, invalidHash
	if err := store.WriteProject(value); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("redundant requested host evidence was accepted: %v", err)
	}
	if err := writeJSON(store.projectPath(), value); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadProject(); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("redundant requested host evidence was accepted while reading: %v", err)
	}
}

func TestStrictStateAndHashValidation(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, _ := spec.Hash(resolved)
	value := ProjectState{Schema: 1, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
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
	value.FarrowVersion = ""
	if err := store.WriteProject(value); err == nil {
		t.Fatal("empty state writer version unexpectedly accepted")
	}
}

func TestTransactionRoundTrip(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	now := time.Now().UTC()
	transaction := Transaction{Schema: 1, FarrowVersion: "dev", OperationID: "op-1", ProjectID: store.Project.Marker.ProjectID, Node: "meta", From: Absent, To: Preparing, Completed: []Action{{Name: "reserve-port", Resource: "2222"}}, StartedAt: now, UpdatedAt: now}
	if err := store.WriteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadTransaction("meta")
	if err != nil || got.OperationID != transaction.OperationID || len(got.Completed) != 1 {
		t.Fatalf("transaction round trip: %#v %v", got, err)
	}
}

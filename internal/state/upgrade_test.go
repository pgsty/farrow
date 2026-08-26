package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
)

func upgradeFixture(t *testing.T) (Store, []string) {
	t.Helper()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, _ := spec.Hash(resolved)
	now := time.Now().UTC()
	projectState := ProjectState{Schema: 1, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: now}
	if err := store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	node := NodeState{
		Schema: 1, FarrowVersion: "dev", ProjectID: store.Project.Marker.ProjectID,
		Node: "meta", VMUUID: "018f4b8e-1234-4abc-9def-0123456789ab", Phase: Prepared,
		Generation: 1, SpecHash: hash, Image: Image{Alias: "u24", Release: "test", Digest: "abc", VirtualSize: 1},
		RootDisk: filepath.Join(store.Project.Root, "nodes", "meta", "root.qcow2"), Seed: filepath.Join(store.Project.Root, "nodes", "meta", "seed.iso"), SSHPort: 2222,
		Forwards: []qemu.Forward{{Bind: "127.0.0.1", Host: 2222, Guest: 22}}, Runtime: RuntimePaths{Directory: "/tmp/farrow", QMP: "/tmp/farrow/qmp.sock", PIDFile: "/tmp/farrow/qemu.pid"},
		Invocation: qemu.Invocation{Binary: "/opt/qemu", Args: []string{"-S"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	transaction := Transaction{Schema: 1, FarrowVersion: "dev", OperationID: "op-1", ProjectID: store.Project.Marker.ProjectID, Node: "meta", From: Absent, To: Preparing, StartedAt: now, UpdatedAt: now}
	if err := store.WriteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(store.Project.Root, "resolved.json"),
		filepath.Join(store.Project.Root, "nodes", "meta", "state.json"),
		filepath.Join(store.Project.Root, "nodes", "meta", "transaction.json"),
	}
	for _, path := range paths {
		downgradeStateFixture(t, path, 0)
	}
	return store, paths
}

func downgradeStateFixture(t *testing.T, path string, schema int) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["schema"] = schema
	delete(object, "farrow_version")
	data, err = json.MarshalIndent(object, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestUpgradeSchemaZeroDryRunBackupApplyAndIdempotence(t *testing.T) {
	t.Parallel()
	store, paths := upgradeFixture(t)
	originals := make(map[string][]byte, len(paths))
	for _, path := range paths {
		originals[path], _ = os.ReadFile(path)
	}
	dry, err := UpgradeProject(context.Background(), store.Project, "v1.0.0", false)
	if err != nil || dry.Apply || len(dry.Actions) != 3 {
		t.Fatalf("dry upgrade=%#v err=%v", dry, err)
	}
	for _, path := range paths {
		actual, _ := os.ReadFile(path)
		if !bytes.Equal(actual, originals[path]) {
			t.Fatalf("dry run changed %s", path)
		}
	}
	for _, action := range dry.Actions {
		if _, err := os.Lstat(action.Backup); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dry run created backup %s: %v", action.Backup, err)
		}
	}
	applied, err := UpgradeProject(context.Background(), store.Project, "v1.0.0", true)
	if err != nil || len(applied.Actions) != 3 {
		t.Fatalf("applied upgrade=%#v err=%v", applied, err)
	}
	for index, action := range applied.Actions {
		if !action.Applied {
			t.Errorf("action %d not applied: %#v", index, action)
		}
		backup, err := os.ReadFile(action.Backup)
		if err != nil || !bytes.Equal(backup, originals[action.Path]) {
			t.Errorf("backup %s mismatch: %v", action.Backup, err)
		}
	}
	projectState, err := store.ReadProject()
	if err != nil || projectState.FarrowVersion != "v1.0.0" {
		t.Fatalf("upgraded project=%#v err=%v", projectState, err)
	}
	node, err := store.ReadNode("meta")
	if err != nil || node.FarrowVersion != "v1.0.0" {
		t.Fatalf("upgraded node=%#v err=%v", node, err)
	}
	transaction, err := store.ReadTransaction("meta")
	if err != nil || transaction.FarrowVersion != "v1.0.0" {
		t.Fatalf("upgraded transaction=%#v err=%v", transaction, err)
	}
	again, err := UpgradeProject(context.Background(), store.Project, "v1.0.0", true)
	if err != nil || len(again.Actions) != 0 || again.Actions == nil {
		t.Fatalf("idempotent upgrade=%#v err=%v", again, err)
	}
}

func TestUpgradeRefusesNewerSchemaWithoutMutation(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	resolved := spec.Quick(true, true)
	hash, _ := spec.Hash(resolved)
	value := ProjectState{Schema: 1, FarrowVersion: "future", ProjectID: store.Project.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
	if err := store.WriteProject(value); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Project.Root, "resolved.json")
	before := downgradeStateFixture(t, path, 2)
	_, err := UpgradeProject(context.Background(), store.Project, "v1.0.0", true)
	var newer *NewerSchemaError
	if !errors.As(err, &newer) || newer.Schema != 2 {
		t.Fatalf("newer schema error=%T %v", err, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("newer schema was overwritten")
	}
	backup, _ := migrationBackupPath(store.Project.Root, path)
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("newer schema created a backup: %v", err)
	}
}

func TestUpgradeRefusesRunningNodeWithoutMutation(t *testing.T) {
	t.Parallel()
	store, paths := upgradeFixture(t)
	nodePath := paths[1]
	data, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["phase"] = "running"
	object["process"] = map[string]any{"pid": 123, "executable": "/opt/qemu", "started": "fixture", "argv_hash": "fixture"}
	data, err = json.MarshalIndent(object, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodePath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	originals := make(map[string][]byte, len(paths))
	for _, path := range paths {
		originals[path], _ = os.ReadFile(path)
	}
	_, err = UpgradeProject(context.Background(), store.Project, "v1.0.0", true)
	if err == nil || !strings.Contains(err.Error(), "requires stopped nodes") {
		t.Fatalf("running node upgrade error = %v", err)
	}
	for _, path := range paths {
		actual, _ := os.ReadFile(path)
		if !bytes.Equal(actual, originals[path]) {
			t.Fatalf("failed running-node preflight changed %s", path)
		}
		backup, _ := migrationBackupPath(store.Project.Root, path)
		if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed running-node preflight created backup %s: %v", backup, err)
		}
	}
}

func TestUpgradePreflightsEveryFileBeforeCreatingBackups(t *testing.T) {
	t.Parallel()
	store, paths := upgradeFixture(t)
	transactionPath := paths[2]
	data, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown"] = true
	data, err = json.MarshalIndent(object, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transactionPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	originals := make(map[string][]byte, len(paths))
	for _, path := range paths {
		originals[path], _ = os.ReadFile(path)
	}
	if _, err := UpgradeProject(context.Background(), store.Project, "v1.0.0", true); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("malformed child state error = %v", err)
	}
	for _, path := range paths {
		actual, _ := os.ReadFile(path)
		if !bytes.Equal(actual, originals[path]) {
			t.Fatalf("failed full preflight changed %s", path)
		}
		backup, _ := migrationBackupPath(store.Project.Root, path)
		if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed full preflight created backup %s: %v", backup, err)
		}
	}
}

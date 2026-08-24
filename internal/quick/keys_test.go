package quick

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/piglet/internal/persistent"
	"github.com/pgsty/piglet/internal/project"
)

func keyPurgeFixture(t *testing.T) (Manager, string, string) {
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
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"id_ed25519": 0o600, "id_ed25519.pub": 0o644, "known_hosts": 0o600} {
		if err := os.WriteFile(filepath.Join(keysDir, name), []byte("owned"), mode); err != nil {
			t.Fatal(err)
		}
	}
	return Manager{CWD: workDir}, projectValue.Root, keysDir
}

func TestPurgeKeysDryRunAndForce(t *testing.T) {
	t.Parallel()
	manager, projectRoot, keysDir := keyPurgeFixture(t)
	dryRun, err := manager.PurgeKeys(context.Background(), false)
	if err != nil || len(dryRun.Actions) != 3 {
		t.Fatalf("dry-run key purge = %#v, %v", dryRun, err)
	}
	if _, err := os.Lstat(filepath.Join(keysDir, "id_ed25519")); err != nil {
		t.Fatalf("dry run removed key: %v", err)
	}
	applied, err := manager.PurgeKeys(context.Background(), true)
	if err != nil || len(applied.Actions) != 3 {
		t.Fatalf("applied key purge = %#v, %v", applied, err)
	}
	if _, err := os.Lstat(keysDir); !os.IsNotExist(err) {
		t.Fatalf("keys directory remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(projectRoot, "project.json")); err != nil {
		t.Fatalf("project marker removed: %v", err)
	}
}

func TestPurgeKeysPreflightPreservesAllOnUnexpectedEntry(t *testing.T) {
	t.Parallel()
	manager, _, keysDir := keyPurgeFixture(t)
	unexpected := filepath.Join(keysDir, "manual-key")
	if err := os.WriteFile(unexpected, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PurgeKeys(context.Background(), true); err == nil {
		t.Fatal("unexpected key entry did not block purge")
	}
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "manual-key"} {
		if _, err := os.Lstat(filepath.Join(keysDir, name)); err != nil {
			t.Fatalf("blocked purge removed %s: %v", name, err)
		}
	}
}

func TestPurgeKeysRejectsRetainedPersistentDisk(t *testing.T) {
	t.Parallel()
	manager, _, keysDir := keyPurgeFixture(t)
	projectValue, err := project.Open(manager.CWD)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(projectValue.Root, "retained-source.qcow2")
	if err := os.WriteFile(source, []byte("qcow-canary"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := persistent.Identity{
		ProjectID: projectValue.Marker.ProjectID, Node: "meta", Name: "data",
		Serial: "abcdefghijklmnopqrst", Size: 8, Mount: "/data", Filesystem: "auto",
	}
	if _, err := persistent.Preserve(projectValue, identity, source); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PurgeKeys(context.Background(), true); err == nil {
		t.Fatal("retained persistent disk did not block key purge")
	}
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts"} {
		if _, err := os.Lstat(filepath.Join(keysDir, name)); err != nil {
			t.Fatalf("blocked purge removed %s: %v", name, err)
		}
	}
}

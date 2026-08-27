package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/project"
)

func TestProjectPruneAndRemoveOrphanRegistration(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	work := filepath.Join(root, "labs", "dev")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	created, err := project.Create(work, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	neutral := filepath.Join(root, "neutral")
	if err := os.Mkdir(neutral, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(neutral); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("FARROW_HOME", dataRoot)

	// Healthy project: prune reports nothing.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"project", "prune"}, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "no orphaned projects") {
		t.Fatalf("healthy prune code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	// Delete the workspace: the registration becomes a removable orphan.
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"project", "prune"}, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "workdir-missing") || !strings.Contains(stdout.String(), "would remove") {
		t.Fatalf("orphan prune code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	// Empty post-destroy residue directories (retained-disk store, keys) must
	// not block removal; the directory names must match the real stores.
	for _, name := range []string{"persistent-disks", "keys", "nodes"} {
		if err := os.Mkdir(filepath.Join(created.Root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// Remove it by ID; the registration directory disappears entirely.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"project", "rm", created.Marker.ProjectID, "--force"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("project rm code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(created.Root); !os.IsNotExist(err) {
		t.Fatalf("registration survived rm: %v", err)
	}
}

func TestProjectPruneYesRemovesOrphans(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	work := filepath.Join(root, "gone")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	created, err := project.Create(work, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	neutral := filepath.Join(root, "neutral")
	if err := os.Mkdir(neutral, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(neutral); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("FARROW_HOME", dataRoot)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"project", "prune", "--yes"}, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "removed "+created.Marker.ProjectID) {
		t.Fatalf("prune --yes code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(created.Root); !os.IsNotExist(err) {
		t.Fatalf("orphan survived prune: %v", err)
	}
}

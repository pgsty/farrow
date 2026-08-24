package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverVerifiedProjectsAndRejectUnsafeEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	for _, name := range []string{"work-a", "work-b"} {
		work := filepath.Join(root, name)
		if err := os.Mkdir(work, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Create(work, dataRoot); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "projects", "not-a-project"), []byte("ignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsafeID, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(dataRoot, "projects", unsafeID)); err != nil {
		t.Fatal(err)
	}

	discovery, err := Discover(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Projects) != 2 || len(discovery.Warnings) != 2 {
		t.Fatalf("discovery = %#v", discovery)
	}
	for _, found := range discovery.Projects {
		if found.Root != filepath.Join(dataRoot, "projects", found.Marker.ProjectID) || found.DataRoot != dataRoot {
			t.Fatalf("unexpected discovered project: %#v", found)
		}
	}
}

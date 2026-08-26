package persistent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/farrow/internal/project"
)

func persistentFixture(t *testing.T) (project.Project, Identity, string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	nodeDir, err := projectValue.EnsureNodeDir("meta")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(nodeDir, "data.qcow2")
	if err := os.WriteFile(source, []byte("retained-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := Identity{ProjectID: projectValue.Marker.ProjectID, Node: "meta", Name: "data", Serial: "disk-serial", Size: 64 << 30, Mount: "/data", Filesystem: "xfs"}
	return projectValue, identity, source
}

func TestPreserveValidateAndExplicitDelete(t *testing.T) {
	t.Parallel()
	projectValue, identity, source := persistentFixture(t)
	record, err := Preserve(projectValue, identity, source)
	if err != nil {
		t.Fatal(err)
	}
	if record.Path == source || record.OwnerUID != os.Geteuid() {
		t.Fatalf("record = %#v", record)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("node-local source remains: %v", err)
	}
	found, err := ValidateDesired(projectValue, []Identity{identity})
	if err != nil || found[key(identity.Node, identity.Name)].Path != record.Path {
		t.Fatalf("validated = %#v, %v", found, err)
	}
	deleted, err := DeleteAll(projectValue)
	if err != nil || len(deleted) != 1 {
		t.Fatalf("deleted = %#v, %v", deleted, err)
	}
	if records, err := Inventory(projectValue); err != nil || len(records) != 0 {
		t.Fatalf("inventory after delete = %#v, %v", records, err)
	}
}

func TestValidateDesiredRejectsIncompatibleSemantics(t *testing.T) {
	t.Parallel()
	projectValue, identity, source := persistentFixture(t)
	if _, err := Preserve(projectValue, identity, source); err != nil {
		t.Fatal(err)
	}
	checks := []func(*Identity){
		func(value *Identity) { value.Size++ },
		func(value *Identity) { value.Mount = "/different" },
		func(value *Identity) { value.Serial = "different" },
	}
	for _, mutate := range checks {
		candidate := identity
		mutate(&candidate)
		if _, err := ValidateDesired(projectValue, []Identity{candidate}); err == nil {
			t.Fatalf("incompatible identity was accepted: %#v", candidate)
		}
	}
}

func TestInventoryFailsClosedOnSymlinkAndUnexpectedArtifact(t *testing.T) {
	t.Parallel()
	t.Run("symlink disk", func(t *testing.T) {
		projectValue, identity, source := persistentFixture(t)
		record, err := Preserve(projectValue, identity, source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(record.Path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(projectValue.Root, "project.json"), record.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := Inventory(projectValue); err == nil {
			t.Fatal("symlink retained disk was accepted")
		}
		if _, err := DeleteAll(projectValue); err == nil {
			t.Fatal("unsafe retained store was deleted")
		}
	})
	t.Run("unexpected file", func(t *testing.T) {
		projectValue, identity, source := persistentFixture(t)
		record, err := Preserve(projectValue, identity, source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(record.Path), "manual.txt"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Inventory(projectValue); err == nil {
			t.Fatal("unexpected retained artifact was accepted")
		}
	})
}

func TestPreserveRejectsForeignHardLinkedSource(t *testing.T) {
	t.Parallel()
	projectValue, identity, source := persistentFixture(t)
	other := filepath.Join(filepath.Dir(source), "other.qcow2")
	if err := os.Link(source, other); err != nil {
		t.Fatal(err)
	}
	if _, err := Preserve(projectValue, identity, source); err == nil {
		t.Fatal("hard-linked source was accepted")
	}
}

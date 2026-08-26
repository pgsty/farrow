package hostshare

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
)

func testProject(t *testing.T) project.Project {
	t.Helper()
	work := t.TempDir()
	data := t.TempDir()
	var err error
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	data, err = filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	markerDir := filepath.Join(work, ".farrow")
	if err := os.Mkdir(markerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return project.Project{WorkDir: work, MarkerDir: markerDir, MarkerPath: filepath.Join(markerDir, "project.json"), DataRoot: data, Root: filepath.Join(data, "projects", "id"), Marker: project.Marker{ProjectID: "id", CreatedAt: time.Now(), DataRoot: data}}
}

func TestOpenRejectsSymlinksAndProtectedPaths(t *testing.T) {
	value := testProject(t)
	source := filepath.Join(value.WorkDir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Validate(value, []spec.Share{{Host: source, Guest: "/mnt/source", Readonly: true}}); err != nil {
		t.Fatalf("valid source: %v", err)
	}
	link := filepath.Join(value.WorkDir, "link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := Validate(value, []spec.Share{{Host: link, Guest: "/mnt/link", Readonly: true}}); err == nil {
		t.Fatal("symlink source was accepted")
	}
	if err := Validate(value, []spec.Share{{Host: value.DataRoot, Guest: "/mnt/data", Readonly: true}}); err == nil {
		t.Fatal("data root source was accepted")
	}
	if err := Validate(value, []spec.Share{{Host: value.WorkDir, Guest: "/mnt/work", Readonly: false}}); err == nil {
		t.Fatal("writable workdir containing marker was accepted")
	}
}

func TestBundleLayoutAndIdentityRecheck(t *testing.T) {
	value := testProject(t)
	source := filepath.Join(value.WorkDir, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	shares := []spec.Share{{Host: source, Guest: "/mnt/source", Readonly: true}}
	bundle, err := Open(value, shares)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	tag := spec.ShareTag(shares[0])
	invocation := qemu.Invocation{InheritedFiles: []qemu.InheritedFile{{FD: 3, Kind: "share", ID: tag}}}
	if err := bundle.ValidateInvocation(invocation, 0); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Recheck(); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(value.WorkDir, "replacement")
	if err := os.Mkdir(replacement, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, source); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Recheck(); err == nil {
		t.Fatal("replaced source identity was accepted")
	}
}

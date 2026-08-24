package project

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeEnv struct {
	values map[string]string
	home   string
}

func (f fakeEnv) Getenv(key string) string     { return f.values[key] }
func (f fakeEnv) UserHomeDir() (string, error) { return f.home, nil }

func TestResolveDataRootSafety(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	home := filepath.Join(root, "home")
	for _, dir := range []string{work, home} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	environment := fakeEnv{home: home, values: map[string]string{"PIGLET_DATA_HOME": filepath.Join(root, "data")}}
	got, err := ResolveDataRoot(work, environment)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "data") {
		t.Fatalf("data root = %s", got)
	}
	for _, unsafe := range []string{home, work, "/", "relative"} {
		environment.values["PIGLET_DATA_HOME"] = unsafe
		if _, err := ResolveDataRoot(work, environment); err == nil {
			t.Errorf("unsafe root %q accepted", unsafe)
		}
	}
}

func TestResolveDataRootWithConfigPrecedence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	home := filepath.Join(root, "home")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(root, "configured")
	xdg := filepath.Join(root, "xdg")
	override := filepath.Join(root, "override")

	environment := fakeEnv{home: home, values: map[string]string{"XDG_DATA_HOME": xdg}}
	got, err := ResolveDataRootWithConfig(work, configured, environment)
	if err != nil || got != configured {
		t.Fatalf("configured root = %q, %v; want %q", got, err, configured)
	}
	environment.values["PIGLET_DATA_HOME"] = override
	got, err = ResolveDataRootWithConfig(work, configured, environment)
	if err != nil || got != override {
		t.Fatalf("environment root = %q, %v; want %q", got, err, override)
	}
	delete(environment.values, "PIGLET_DATA_HOME")
	got, err = ResolveDataRootWithConfig(work, "", environment)
	if err != nil || got != filepath.Join(xdg, "piglet") {
		t.Fatalf("XDG root = %q, %v", got, err)
	}

	for _, unsafe := range []string{"relative", "/", home, work, xdg} {
		if _, err := ResolveDataRootWithConfig(work, unsafe, environment); err == nil {
			t.Errorf("unsafe configured root %q accepted", unsafe)
		}
	}
}

func TestCreateOpenAndNodePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	data := filepath.Join(root, "data")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	created, err := Create(work, data)
	if err != nil {
		t.Fatal(err)
	}
	if !uuidPattern.MatchString(created.Marker.ProjectID) {
		t.Fatalf("project UUID = %q", created.Marker.ProjectID)
	}
	opened, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Marker != created.Marker || opened.Root != created.Root {
		t.Fatalf("opened project mismatch: %#v %#v", opened, created)
	}
	node, err := opened.EnsureNodeDir("meta")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(node); err != nil || !info.IsDir() {
		t.Fatalf("node dir missing: %v", err)
	}
	if _, err := opened.NodeDir("../escape"); err == nil {
		t.Fatal("unsafe node accepted")
	}
}

func TestOpenRejectsUnknownMarkerFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	data := filepath.Join(root, "data")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	project, err := Create(work, data)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte(fmt.Sprintf(`{"schema":1,"project_id":%q,"created_at":%q,"data_root":%q,"unknown":true}`, project.Marker.ProjectID, project.Marker.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), data))
	if err := os.WriteFile(project.MarkerPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(work); err == nil {
		t.Fatal("unknown marker field accepted")
	}
}

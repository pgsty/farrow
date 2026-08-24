package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/piglet/internal/project"
)

func TestProjectChecksAreReadOnlyAndReportCurrentProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(workDir, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(projectValue.Root)
	if err != nil {
		t.Fatal(err)
	}
	checks := (Probe{CWD: workDir}).projectChecks()
	after, err := os.ReadDir(projectValue.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) < 3 || len(before) != len(after) {
		t.Fatalf("checks=%#v before=%v after=%v", checks, before, after)
	}
	foundProject := false
	for _, check := range checks {
		if check.Name == "project" && check.Status == OK {
			foundProject = true
		}
	}
	if !foundProject {
		t.Fatalf("project check missing: %#v", checks)
	}
}

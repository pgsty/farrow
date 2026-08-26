package quick

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanCreateDoesNotMutateWorkspace(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	manager := Manager{CWD: work, FarrowVersion: "dev"}
	plan, err := manager.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != "create" || plan.ProjectExists {
		t.Fatalf("plan = %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(work, ".farrow")); !os.IsNotExist(err) {
		t.Fatalf("read-only plan mutated workspace: %v", err)
	}
}

package doctor

import (
	"os"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func TestDeploymentChecksAreReadOnlyAndReportCurrentDeployment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	store := state.Store{Root: root}
	resolved := spec.Quick(true, true)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	deployment := state.DeploymentState{Schema: state.DeploymentSchema, FarrowVersion: "dev", SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
	if err := store.WriteDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checks := (Probe{}).deploymentChecks()
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) < 2 || len(before) != len(after) {
		t.Fatalf("checks=%#v before=%v after=%v", checks, before, after)
	}
	foundDeployment := false
	foundDataRoot := false
	for _, check := range checks {
		if check.Name == "deployment" && check.Status == OK {
			foundDeployment = true
		}
		if check.Name == "data-root" {
			foundDataRoot = true
		}
	}
	if !foundDeployment || !foundDataRoot {
		t.Fatalf("deployment or data-root check missing: %#v", checks)
	}
}

func TestDeploymentChecksReportMissingStateAsOK(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	checks := (Probe{}).deploymentChecks()
	for _, check := range checks {
		if check.Name == "deployment" && check.Status != OK {
			t.Fatalf("absent deployment state should not be an error: %#v", check)
		}
	}
}

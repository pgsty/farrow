package linux

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageInstallPlanWritesOnlyMappedRegularFiles(t *testing.T) {
	t.Parallel()
	plan, err := NewInstallPlan(debianFacts(), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(t.TempDir(), "stage")
	result, err := StageInstallPlan(plan, staging)
	if err != nil {
		t.Fatal(err)
	}
	if result.Schema != 1 || len(result.Targets) != len(plan.Files) {
		t.Fatalf("staged plan = %#v", result)
	}
	for path, target := range result.Targets {
		info, err := os.Lstat(target.Source)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("target %s source is unsafe: %v %v", path, info, err)
		}
		if digest, err := stagedFileDigest(target.Source); err != nil || digest != target.SHA256 {
			t.Fatalf("target %s digest = %s, %v", path, digest, err)
		}
	}
	planInfo, err := os.Lstat(filepath.Join(staging, "install-plan.json"))
	if err != nil || planInfo.Mode().Perm() != 0o600 {
		t.Fatalf("staged plan metadata mode = %v, %v", planInfo, err)
	}
}

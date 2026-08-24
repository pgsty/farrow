package pigsty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fullFixture = `---
all:
  children:
    infra: { hosts: { 10.10.10.10: { infra_seq: 1 } } }
    pg-test:
      hosts:
        10.10.10.11: { pg_seq: 1 }
        10.10.10.12: { pg_seq: 2 }
        10.10.10.13: { pg_seq: 3 }
      vars:
        pg_vip_address: 10.10.10.3/24
    redis:
      hosts:
        10.10.10.10: { redis_instances: { 6379: { replica_of: '10.10.10.12 6379' } } }
  vars:
    admin_ip: 10.10.10.10
    infra_portal:
      db: { endpoint: "http://10.10.10.11:5432/path" }
    external: 192.168.0.10
# comment 10.10.10.2/24 and embedded x10.10.10.99 remain bounded
`

func fixtureRoot(t *testing.T, profilePath, content string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(profilePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestRenderRebasesCompleteDefaultSubnetTokens(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t, "conf/ha/full.yml", fullFixture)
	result, err := Render(root, "full", "172.31.251.0/24")
	if err != nil {
		t.Fatal(err)
	}
	output := string(result.Data)
	for _, value := range []string{"172.31.251.10", "172.31.251.11", "172.31.251.12", "172.31.251.13", "172.31.251.3/24"} {
		if !strings.Contains(output, value) {
			t.Errorf("rebased inventory is missing %s\n%s", value, output)
		}
	}
	if strings.Contains(output, "x10.10.10.99") || !strings.Contains(output, "192.168.0.10") {
		t.Fatalf("source comments remained or external address changed:\n%s", output)
	}
	if result.Matches != 9 || result.Replacements != 9 || result.TargetCIDR != "172.31.251.0/24" {
		t.Fatalf("result=%#v", result)
	}
}

func TestRenderDefaultIsByteIdentical(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t, "conf/ha/full.yml", fullFixture)
	result, err := Render(root, "full", "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Data, []byte(fullFixture)) || result.Replacements != 0 {
		t.Fatalf("default render changed bytes: %#v", result)
	}
}

func TestRenderRejectsMismatchedProfileAndUnsafeSources(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t, "conf/ha/full.yml", strings.ReplaceAll(fullFixture, "10.10.10.10", "10.10.10.20"))
	if _, err := Render(root, "full", "172.31.251.0/24"); err == nil || !strings.Contains(err.Error(), "host mismatch") {
		t.Fatalf("mismatched profile error=%v", err)
	}
	if _, err := Render(root, "full", "8.8.8.0/24"); err == nil {
		t.Fatal("public target subnet was accepted")
	}
	if _, err := Render("relative", "full", "172.31.251.0/24"); err == nil {
		t.Fatal("relative source root was accepted")
	}

	symlinkRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(symlinkRoot, "conf", "ha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "conf", "ha", "full.yml"), filepath.Join(symlinkRoot, "conf", "ha", "full.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(symlinkRoot, "full", "172.31.251.0/24"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink source error=%v", err)
	}
}

func TestRenderRejectsMalformedAndDuplicateYAML(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		"all: [10.10.10.10\n",
		"all:\n  vars: {admin_ip: 10.10.10.10}\n  vars: {admin_ip: 10.10.10.11}\n",
		"all: &all\n  vars: {admin_ip: 10.10.10.10}\ncopy: *all\n",
	} {
		root := fixtureRoot(t, "conf/ha/full.yml", content)
		if _, err := Render(root, "full", "172.31.251.0/24"); err == nil {
			t.Fatalf("unsafe YAML was accepted:\n%s", content)
		}
	}
}

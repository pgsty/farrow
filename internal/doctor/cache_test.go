package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/project"
)

func cacheChecksFixture() []Check {
	return []Check{
		{Name: "accelerator", Status: OK, Evidence: "hvf"},
		{Name: "machine", Status: OK, Evidence: "virt"},
		{Name: "cpu", Status: OK, Evidence: "host"},
		{Name: "devices", Status: OK, Evidence: "virtio"},
		{Name: "netdev", Status: OK, Evidence: "user"},
	}
}

func TestCapabilityCacheExactKeyAndBinaryInvalidation(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(work, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "qemu-system-test")
	if err := os.WriteFile(binary, []byte("binary-v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := capabilityKeyFor(binary, "11.1.0")
	if err != nil {
		t.Fatal(err)
	}
	probe := Probe{CWD: work}
	if err := probe.writeCapabilityCache(key, cacheChecksFixture()); err != nil {
		t.Fatal(err)
	}
	checks, hit, err := probe.loadCapabilityCache(key)
	if err != nil || !hit || len(checks) != 5 || !strings.HasPrefix(checks[0].Evidence, "cached for exact QEMU") {
		t.Fatalf("cache hit=%t checks=%#v err=%v", hit, checks, err)
	}
	if err := os.WriteFile(binary, []byte("binary-v2-is-different"), 0o700); err != nil {
		t.Fatal(err)
	}
	changed, err := capabilityKeyFor(binary, "11.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, hit, err := probe.loadCapabilityCache(changed); err != nil || hit {
		t.Fatalf("changed binary cache hit=%t err=%v", hit, err)
	}
	cachePath, err := probe.capabilityCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(cachePath); err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("cache metadata=%#v err=%v", info, err)
	}
}

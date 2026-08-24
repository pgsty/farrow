package pigsty

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishManagedInventoryLifecycle(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t, "conf/ha/full.yml", fullFixture)
	custom, err := Render(root, "full", "172.31.251.0/24")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := Render(root, "full", "")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "managed", "pigsty.yml")
	if err := os.Mkdir(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := Publish(output, custom, false)
	if err != nil || !first.Changed {
		t.Fatalf("first publish=%#v err=%v", first, err)
	}
	for _, path := range []string{output, output + ".piglet.json"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("managed path %s info=%v err=%v", path, info, err)
		}
	}
	if same, err := Publish(output, custom, false); err != nil || same.Changed {
		t.Fatalf("idempotent publish=%#v err=%v", same, err)
	}
	if _, err := Publish(output, baseline, false); !errors.Is(err, ErrOutputConflict) {
		t.Fatalf("changed desired output error=%v", err)
	}
	if changed, err := Publish(output, baseline, true); err != nil || !changed.Changed {
		t.Fatalf("forced managed update=%#v err=%v", changed, err)
	}
	if _, err := Publish(baseline.SourcePath, baseline, true); !errors.Is(err, ErrOutputIntegrity) {
		t.Fatalf("source overwrite error=%v", err)
	}
}

func TestPublishRejectsUnmanagedAndTamperedOutput(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t, "conf/ha/full.yml", fullFixture)
	rendered, err := Render(root, "full", "172.31.251.0/24")
	if err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(root, "unmanaged.yml")
	if err := os.WriteFile(unmanaged, []byte("unmanaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(unmanaged, rendered, true); !errors.Is(err, ErrOutputIntegrity) {
		t.Fatalf("unmanaged output error=%v", err)
	}

	managed := filepath.Join(root, "managed.yml")
	if _, err := Publish(managed, rendered, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(managed, rendered, true); !errors.Is(err, ErrOutputIntegrity) {
		t.Fatalf("tampered output error=%v", err)
	}
}

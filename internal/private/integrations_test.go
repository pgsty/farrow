package private

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/lock"
)

func TestPrivateSSHConfigInstallContainsEveryNodeAndAddress(t *testing.T) {
	t.Parallel()
	startConfig, _ := preparedStartFixture(t)
	keysDir := filepath.Join(startConfig.Project.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"id_ed25519": 0o600, "known_hosts": 0o600} {
		if err := os.WriteFile(filepath.Join(keysDir, name), []byte("fixture"), mode); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	manager := Manager{CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	standalone, err := manager.SSHConfig(context.Background())
	if err != nil || !strings.Contains(standalone, "Host meta 10.10.10.10 admin.example") {
		t.Fatalf("standalone SSH config=%q err=%v", standalone, err)
	}
	if !strings.Contains(standalone, `IdentityFile "`) || !strings.Contains(standalone, `UserKnownHostsFile "`) {
		t.Fatalf("standalone SSH paths are not quoted: %q", standalone)
	}
	selectedManager := manager
	selectedManager.Nodes = []string{"node-1"}
	selected, err := selectedManager.SSHConfig(context.Background())
	if err != nil || !strings.Contains(selected, "Host node-1 10.10.10.11") || strings.Contains(selected, "Host meta ") {
		t.Fatalf("selected SSH config=%q err=%v", selected, err)
	}
	result, err := manager.InstallSSHConfig(context.Background(), "lab", home)
	if err != nil || !result.Changed {
		t.Fatalf("install result=%#v err=%v", result, err)
	}
	fragment, err := os.ReadFile(result.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Host lab-meta meta 10.10.10.10 admin.example", "Host lab-node-1 node-1 10.10.10.11", "Port 2222", "Port 2223"} {
		if !strings.Contains(string(fragment), expected) {
			t.Fatalf("private fragment lacks %q:\n%s", expected, fragment)
		}
	}
	second, err := manager.InstallSSHConfig(context.Background(), "lab", home)
	if err != nil || second.Changed {
		t.Fatalf("idempotent install result=%#v err=%v", second, err)
	}
	removed, err := manager.RemoveSSHConfig("lab", home)
	if err != nil || !removed.Changed {
		t.Fatalf("remove result=%#v err=%v", removed, err)
	}
}

func TestPrivateHostEntriesIncludeDeclaredAliases(t *testing.T) {
	t.Parallel()
	startConfig, _ := preparedStartFixture(t)
	manager := Manager{CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	projectID, entries, err := manager.HostEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if projectID != startConfig.Project.Marker.ProjectID || len(entries) != 2 {
		t.Fatalf("host entries project=%q entries=%#v", projectID, entries)
	}
	if entries[0].Address != "10.10.10.10" || strings.Join(entries[0].Names, ",") != "meta,admin.example" || entries[1].Address != "10.10.10.11" || strings.Join(entries[1].Names, ",") != "node-1" {
		t.Fatalf("host entries = %#v", entries)
	}
}

func TestPrivateSSHConfigRejectsSymlinkedKeysDirectory(t *testing.T) {
	t.Parallel()
	startConfig, _ := preparedStartFixture(t)
	outside := t.TempDir()
	for name := range map[string]struct{}{"id_ed25519": {}, "known_hosts": {}} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(startConfig.Project.Root, "keys")); err != nil {
		t.Fatal(err)
	}
	manager := Manager{CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	if _, err := manager.InstallSSHConfig(context.Background(), "lab", t.TempDir()); err == nil {
		t.Fatal("symlinked project keys directory was accepted")
	}
}

func TestConnectionsLockedRequiresAndReusesExclusiveProjectLock(t *testing.T) {
	startConfig, _ := preparedStartFixture(t)
	keysDir := filepath.Join(startConfig.Project.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id_ed25519", "known_hosts"} {
		if err := os.WriteFile(filepath.Join(keysDir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manager := Manager{CWD: startConfig.Project.WorkDir, FarrowVersion: "test", LeaseStore: &startConfig.LeaseStore}
	if _, err := manager.ConnectionsLocked(context.Background(), startConfig.Project, nil); err == nil || !strings.Contains(err.Error(), "exclusive project lock") {
		t.Fatalf("missing token error = %v", err)
	}
	projectLock, err := lock.Acquire(context.Background(), filepath.Join(startConfig.Project.Root, "project.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer projectLock.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = manager.ConnectionsLocked(ctx, startConfig.Project, projectLock)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("locked snapshot error = %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("locked helper attempted to reacquire its own project lock: %v", ctx.Err())
	}
}

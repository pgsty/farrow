package quick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/lease"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/spec"
)

func TestChoosePortsMaterializesConflict(t *testing.T) {
	probe := func(port uint16) bool { return port != 15432 }
	sshPort, resolved, err := choosePortsWithProbe(spec.Quick(true, true), map[uint16]struct{}{2222: {}}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if sshPort != 12222 {
		t.Fatalf("SSH port = %d, want 12222", sshPort)
	}
	if resolved.Nodes[0].Forwards[0].Host != 25432 {
		t.Fatalf("PostgreSQL port = %d, want 25432", resolved.Nodes[0].Forwards[0].Host)
	}
}

func TestDataRootPrefersPersistedProjectMarker(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	persisted := filepath.Join(root, "persisted-data")
	if _, err := project.Create(workDir, persisted); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIGLET_DATA_HOME", filepath.Join(root, "different-environment-root"))
	actual, err := (Manager{CWD: workDir}).dataRoot()
	if err != nil || actual != persisted {
		t.Fatalf("data root = %q, %v; want persisted %q", actual, err, persisted)
	}
}

func TestPlanMaterializesConfiguredDataRootWithEnvironmentPrecedence(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	environmentRoot := filepath.Join(root, "environment-data")
	t.Setenv("PIGLET_DATA_HOME", environmentRoot)
	desired := spec.Quick(true, true)
	desired.DataRoot = filepath.Join(root, "configured-data")
	plan, err := (Manager{CWD: workDir}).PlanResolved(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != "create" || plan.After.DataRoot != environmentRoot {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestConfiguredDataRootChangeRequiresMigration(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(workDir, filepath.Join(root, "persisted-data")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIGLET_DATA_HOME", "")
	desired := spec.Quick(true, true)
	desired.DataRoot = filepath.Join(root, "different-data")
	_, err := (Manager{CWD: workDir}).PlanResolved(context.Background(), desired)
	if err == nil || !errors.Is(err, project.ErrDataRootMigrationRequired) || !strings.Contains(err.Error(), "data-root migration") {
		t.Fatalf("migration error = %v", err)
	}
}

func TestQuickManagerUsesResolvedReadinessTimeout(t *testing.T) {
	t.Parallel()
	resolved := spec.Quick(true, true)
	resolved.SSHWaitTimeoutNS = int64(750 * time.Millisecond)
	timeout, err := (Manager{}).readyTimeout(resolved)
	if err != nil || timeout != 750*time.Millisecond {
		t.Fatalf("resolved timeout = %s, %v", timeout, err)
	}
	timeout, err = (Manager{ReadyTimeout: 2 * time.Second}).readyTimeout(resolved)
	if err != nil || timeout != 2*time.Second {
		t.Fatalf("manager override timeout = %s, %v", timeout, err)
	}
	resolved.SSHWaitTimeoutNS = -1
	if _, err := (Manager{}).readyTimeout(resolved); err == nil {
		t.Fatal("negative resolved timeout accepted")
	}
}

func TestReservedPortsFailsClosedOnCorruptRegisteredState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	nodeDir, err := projectValue.EnsureNodeDir("meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "state.json"), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reservedPorts(projectValue.DataRoot); err == nil {
		t.Fatal("corrupt registered state was ignored during port allocation")
	}
}

func TestQEMULogLevelFiltering(t *testing.T) {
	t.Parallel()
	manager := Manager{LogLevel: "warn"}
	for level, want := range map[string]bool{"error": true, "warn": true, "info": false, "debug": false} {
		got, err := manager.qemuLogEnabled(level)
		if err != nil || got != want {
			t.Errorf("level %s = %t, %v; want %t", level, got, err, want)
		}
	}
	if _, err := (Manager{LogLevel: "verbose"}).qemuLogEnabled("info"); err == nil {
		t.Fatal("invalid configured log level accepted")
	}
}

func TestPrivateUpCapabilityGateIsReadOnly(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	missingRoot := filepath.Join(t.TempDir(), "not-installed")
	leaseStore := lease.Store{Root: missingRoot, OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid()}
	resolved := spec.Resolved{
		Schema: 1, Name: "full", Image: "u24", Network: "private", SSHUser: "dba",
		Private: &spec.PrivateNetwork{CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8"},
		Nodes: []spec.Node{
			{Name: "meta", Control: true, Address: "10.10.10.10", CPUs: 1, Memory: spec.GiB, RootDisk: 8 * spec.GiB},
			{Name: "node-1", Address: "10.10.10.11", CPUs: 1, Memory: spec.GiB, RootDisk: 8 * spec.GiB},
		},
	}
	_, err := (Manager{CWD: workDir, LeaseStore: &leaseStore}).UpResolvedWithPolicy(context.Background(), resolved, UpPolicy{})
	var capability *CapabilityError
	if !errors.As(err, &capability) {
		t.Fatalf("private up error = %T %v", err, err)
	}
	if _, err := os.Lstat(filepath.Join(workDir, ".piglet")); !os.IsNotExist(err) {
		t.Fatalf("capability preflight mutated workspace: %v", err)
	}
}

func TestRemoveOwnedRegularBoundaries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	owned := filepath.Join(root, "owned")
	if err := os.WriteFile(owned, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := removeOwnedRegular(root, outside); err == nil {
		t.Fatal("outside target unexpectedly accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(owned, link); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedRegular(root, link); err == nil {
		t.Fatal("symlink target unexpectedly accepted")
	}
	if err := removeOwnedRegular(root, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("owned target still exists: %v", err)
	}
}

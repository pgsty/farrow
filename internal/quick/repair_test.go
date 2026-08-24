package quick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/state"
)

func TestRepairOrphanDryRunAndApply(t *testing.T) {
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
	nodeDir, err := projectValue.EnsureNodeDir(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	rootDisk := filepath.Join(nodeDir, "root.qcow2")
	dataDisk := filepath.Join(nodeDir, "data.qcow2")
	for _, path := range []string{rootDisk, dataDisk} {
		if err := os.WriteFile(path, []byte("transaction-owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keys := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(keys, "id_ed25519")
	if err := os.WriteFile(key, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	transaction := state.Transaction{Schema: 1, PigletVersion: "dev", OperationID: "op", ProjectID: projectValue.Marker.ProjectID, Node: nodeName, From: state.Absent, To: state.Preparing, Completed: []state.Action{{Name: "project-key", Resource: keys}, {Name: "root-overlay", Resource: rootDisk}, {Name: "data-disk", Resource: dataDisk}}, StartedAt: now, UpdatedAt: now}
	if err := (state.Store{Project: projectValue}).WriteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	manager := Manager{CWD: work, PigletVersion: "dev"}
	dryRun, err := manager.Repair(context.Background(), false)
	if err != nil || len(dryRun.Actions) < 3 {
		t.Fatalf("dry run = %#v, %v", dryRun, err)
	}
	for _, path := range []string{rootDisk, dataDisk, key} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry run removed %s: %v", path, err)
		}
	}
	applied, err := manager.Repair(context.Background(), true)
	if err != nil || !applied.Apply {
		t.Fatalf("apply = %#v, %v", applied, err)
	}
	for _, path := range []string{rootDisk, dataDisk, filepath.Join(nodeDir, "transaction.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("rollback target remains %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(key); err != nil || string(data) != "preserve" {
		t.Fatalf("project key was not preserved: %q %v", data, err)
	}
}

type orphanRepairFixture struct {
	manager  Manager
	store    state.Store
	nodeDir  string
	rootDisk string
}

func newOrphanRepairFixture(t *testing.T) orphanRepairFixture {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	nodeDir, err := projectValue.EnsureNodeDir(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	return orphanRepairFixture{
		manager:  Manager{CWD: work, PigletVersion: "dev"},
		store:    state.Store{Project: projectValue},
		nodeDir:  nodeDir,
		rootDisk: filepath.Join(nodeDir, "root.qcow2"),
	}
}

func (fixture orphanRepairFixture) writeTransaction(t *testing.T, completed ...state.Action) {
	t.Helper()
	now := time.Now().UTC()
	transaction := state.Transaction{
		Schema: state.TransactionSchema, PigletVersion: "dev", OperationID: "op",
		ProjectID: fixture.store.Project.Marker.ProjectID, Node: nodeName,
		From: state.Absent, To: state.Preparing, Completed: completed,
		StartedAt: now, UpdatedAt: now,
	}
	if err := fixture.store.WriteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
}

func assertRepairBlocked(t *testing.T, report RepairReport, err error) {
	t.Helper()
	var blocked *RepairBlockedError
	if !errors.As(err, &blocked) || !report.Blocked {
		t.Fatalf("repair = %#v, %v; want blocked integrity error", report, err)
	}
	for _, action := range report.Actions {
		if action.Applied {
			t.Fatalf("blocked repair reported an applied action: %#v", action)
		}
	}
}

func TestRepairOrphanPreflightPreservesAllOnUnexpectedEntry(t *testing.T) {
	t.Parallel()
	fixture := newOrphanRepairFixture(t)
	unexpected := filepath.Join(fixture.nodeDir, "manual.qcow2")
	for _, path := range []string{fixture.rootDisk, unexpected} {
		if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.writeTransaction(t, state.Action{Name: "root-overlay", Resource: fixture.rootDisk})

	report, err := fixture.manager.Repair(context.Background(), true)
	assertRepairBlocked(t, report, err)
	for _, path := range []string{fixture.rootDisk, unexpected, filepath.Join(fixture.nodeDir, "transaction.json")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("preflight removed %s: %v", path, err)
		}
	}
}

func TestRepairOrphanPreflightRejectsSymlinkWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	fixture := newOrphanRepairFixture(t)
	external := filepath.Join(t.TempDir(), "external.qcow2")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, fixture.rootDisk); err != nil {
		t.Fatal(err)
	}
	fixture.writeTransaction(t, state.Action{Name: "root-overlay", Resource: fixture.rootDisk})

	report, err := fixture.manager.Repair(context.Background(), true)
	assertRepairBlocked(t, report, err)
	if info, err := os.Lstat(fixture.rootDisk); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("owned-path symlink was changed: %v %v", info, err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "external" {
		t.Fatalf("external target was changed: %q %v", data, err)
	}
}

func TestRepairOrphanPreflightPreservesResourcesForLivePID(t *testing.T) {
	runtimeParent := "/tmp"
	if runtime.GOOS == "darwin" {
		runtimeParent = "/private/tmp"
	}
	runtimeRoot, err := os.MkdirTemp(runtimeParent, "qr.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeRoot)
	fixture := newOrphanRepairFixture(t)
	if err := os.WriteFile(fixture.rootDisk, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.writeTransaction(t, state.Action{Name: "root-overlay", Resource: fixture.rootDisk})
	runtimeDir, _, pidPath, err := orphanRuntime(fixture.store.Project.Marker.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRuntime(runtimeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(pidPath)
		_ = os.Remove(runtimeDir)
	})

	report, err := fixture.manager.Repair(context.Background(), true)
	assertRepairBlocked(t, report, err)
	for _, path := range []string{fixture.rootDisk, filepath.Join(fixture.nodeDir, "transaction.json"), pidPath} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("live-PID preflight removed %s: %v", path, err)
		}
	}
}

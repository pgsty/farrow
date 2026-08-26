package quick

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func absenceFixture(t *testing.T) (Manager, project.Project, state.ProjectState) {
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
	resolved := spec.Quick(true, true)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	projectState := state.ProjectState{Schema: state.ProjectSchema, FarrowVersion: "test", ProjectID: projectValue.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}
	if err := (state.Store{Project: projectValue}).WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	return Manager{CWD: work, FarrowVersion: "test"}, projectValue, projectState
}

func TestStatusReportsAbsentOnlyWithValidDestroyRecord(t *testing.T) {
	t.Parallel()
	manager, projectValue, projectState := absenceFixture(t)
	if _, err := manager.Status(context.Background()); err == nil {
		t.Fatal("missing node without absence record was treated as destroyed")
	}
	if err := writeAbsence(projectValue, projectState); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil || status.State != state.Absent || status.Node != nodeName || status.SSHUser != "dba" || status.SSHHost != "" || status.SSHPort != 0 {
		t.Fatalf("absent status = %#v, %v", status, err)
	}
	if err := clearAbsence(projectValue); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(absencePath(projectValue)); !os.IsNotExist(err) {
		t.Fatalf("absence record remains: %v", err)
	}
}

func TestAbsenceRecordStrictIdentity(t *testing.T) {
	t.Parallel()
	_, projectValue, projectState := absenceFixture(t)
	if err := writeAbsence(projectValue, projectState); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absencePath(projectValue), []byte(`{"schema":1,"project_id":"wrong","node":"meta","spec_hash":"wrong","destroyed_at":"2026-08-24T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAbsence(projectValue, projectState); err == nil {
		t.Fatal("wrong absence identity was accepted")
	}
}

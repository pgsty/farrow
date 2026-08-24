package quick

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/state"
)

const absenceSchema = 1

type absenceRecord struct {
	Schema      int       `json:"schema"`
	ProjectID   string    `json:"project_id"`
	Node        string    `json:"node"`
	SpecHash    string    `json:"spec_hash"`
	DestroyedAt time.Time `json:"destroyed_at"`
}

func absencePath(projectValue project.Project) string {
	return filepath.Join(projectValue.Root, "quick-absent.json")
}

func writeAbsence(projectValue project.Project, projectState state.ProjectState) error {
	record := absenceRecord{Schema: absenceSchema, ProjectID: projectValue.Marker.ProjectID, Node: nodeName, SpecHash: projectState.SpecHash, DestroyedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(absencePath(projectValue), append(data, '\n'), 0o600)
}

func readAbsence(projectValue project.Project, projectState state.ProjectState) (absenceRecord, error) {
	path := absencePath(projectValue)
	info, err := os.Lstat(path)
	if err != nil {
		return absenceRecord{}, errors.New("quick node state is missing without a safe absence record")
	}
	stat, statOK := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > 4096 || !statOK || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 {
		return absenceRecord{}, errors.New("quick node state is missing without a safe absence record")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return absenceRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record absenceRecord
	if err := decoder.Decode(&record); err != nil {
		return absenceRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return absenceRecord{}, errors.New("quick absence record contains trailing JSON")
	}
	if record.Schema != absenceSchema || record.ProjectID != projectValue.Marker.ProjectID || record.Node != nodeName || record.SpecHash != projectState.SpecHash || record.DestroyedAt.IsZero() {
		return absenceRecord{}, errors.New("quick absence record identity is invalid")
	}
	return record, nil
}

func clearAbsence(projectValue project.Project) error {
	path := absencePath(projectValue)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return errors.New("refuse removal of unsafe quick absence record")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return fsutil.SyncDir(projectValue.Root)
}

func absentStatus(projectValue project.Project, projectState state.ProjectState, message string) Status {
	node := nodeName
	if len(projectState.Resolved.Nodes) > 0 {
		node = projectState.Resolved.Nodes[0].Name
	}
	return Status{ProjectID: projectValue.Marker.ProjectID, Node: node, State: state.Absent, SSHUser: statusSSHUser(projectState.Resolved.SSHUser), SpecHash: projectState.SpecHash, Message: message}
}

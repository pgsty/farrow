package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/project"
)

type NewerSchemaError struct {
	Path   string
	Schema int
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("state %s uses newer schema %d; this binary will not overwrite it", e.Path, e.Schema)
}

type UpgradeAction struct {
	Path       string `json:"path"`
	Backup     string `json:"backup"`
	FromSchema int    `json:"from_schema"`
	ToSchema   int    `json:"to_schema"`
	Applied    bool   `json:"applied"`
}

type UpgradeReport struct {
	Schema    int             `json:"schema"`
	ProjectID string          `json:"project_id"`
	Apply     bool            `json:"apply"`
	Actions   []UpgradeAction `json:"actions"`
}

type upgradeCandidate struct {
	path      string
	backup    string
	original  []byte
	upgraded  []byte
	from      int
	to        int
	validator func([]byte) error
}

func readUpgradeSource(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("state upgrade source is missing or unsafe: %s", path)
	}
	statistics, hasStatistics := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > maxStateBytes || !hasStatistics || statistics.Uid != uint32(os.Getuid()) || statistics.Nlink != 1 {
		return nil, fmt.Errorf("state upgrade source is missing or unsafe: %s", path)
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("state upgrade source identity changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxStateBytes+1))
	if err != nil || len(data) > maxStateBytes {
		return nil, errors.New("state upgrade source exceeds size limit")
	}
	return data, nil
}

const maxStateBytes = 1 << 20

func strictObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("state upgrade source contains trailing JSON")
	}
	if object == nil {
		return nil, errors.New("state upgrade source is not an object")
	}
	return object, nil
}

func objectSchema(object map[string]json.RawMessage) (int, error) {
	raw, ok := object["schema"]
	if !ok {
		return 0, errors.New("state upgrade source has no schema")
	}
	var schema int
	if err := json.Unmarshal(raw, &schema); err != nil || schema < 0 {
		return 0, errors.New("state upgrade schema is invalid")
	}
	return schema, nil
}

func injectV1(data []byte, version string) ([]byte, int, error) {
	object, err := strictObject(data)
	if err != nil {
		return nil, 0, err
	}
	schema, err := objectSchema(object)
	if err != nil {
		return nil, 0, err
	}
	if schema > 1 {
		return nil, schema, nil
	}
	if schema == 1 {
		return append([]byte(nil), data...), schema, nil
	}
	if _, exists := object["farrow_version"]; exists {
		return nil, 0, errors.New("schema-0 state unexpectedly contains farrow_version")
	}
	schemaJSON, _ := json.Marshal(1)
	versionJSON, _ := json.Marshal(version)
	object["schema"] = schemaJSON
	object["farrow_version"] = versionJSON
	upgraded, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	return append(upgraded, '\n'), schema, nil
}

func decodeStrictBytes(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("upgraded state contains trailing JSON")
	}
	return nil
}

func projectValidator(projectID string, destination *ProjectState) func([]byte) error {
	return func(data []byte) error {
		var value ProjectState
		if err := decodeStrictBytes(data, &value); err != nil {
			return err
		}
		if err := validateProject(value, projectID); err != nil {
			return err
		}
		*destination = value
		return nil
	}
}

func nodeValidator(projectID, node string, destination *NodeState) func([]byte) error {
	return func(data []byte) error {
		var value NodeState
		if err := decodeStrictBytes(data, &value); err != nil {
			return err
		}
		if err := validateNode(value, projectID, node); err != nil {
			return err
		}
		*destination = value
		return nil
	}
}

func validateUpgradeStopped(value NodeState) error {
	if value.Phase != Absent && value.Phase != Prepared && value.Phase != Stopped {
		return fmt.Errorf("state upgrade requires stopped nodes; node %s is %s", value.Node, value.Phase)
	}
	if value.Process != (ProcessIdentity{}) {
		return fmt.Errorf("state upgrade requires an empty process identity; node %s still records pid %d", value.Node, value.Process.PID)
	}
	return nil
}

func transactionValidator(projectID, node string) func([]byte) error {
	return func(data []byte) error {
		var value Transaction
		if err := decodeStrictBytes(data, &value); err != nil {
			return err
		}
		if value.Node != node {
			return errors.New("transaction node differs from its state directory")
		}
		return validateTransaction(value, projectID)
	}
}

func migrationBackupPath(projectRoot, path string) (string, error) {
	relative, err := filepath.Rel(projectRoot, path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || len(relative) > 512 {
		return "", errors.New("state migration source escapes the project root")
	}
	for _, component := range []string{".." + string(filepath.Separator), string(filepath.Separator) + ".."} {
		if bytes.Contains([]byte(relative), []byte(component)) {
			return "", errors.New("state migration source escapes the project root")
		}
	}
	name := "migration-schema0-" + strings.ReplaceAll(relative, string(filepath.Separator), "__") + ".bak"
	return filepath.Join(projectRoot, name), nil
}

func planCandidate(projectRoot, path, version string, validator func([]byte) error) (*upgradeCandidate, error) {
	original, err := readUpgradeSource(path)
	if err != nil {
		return nil, err
	}
	upgraded, schema, err := injectV1(original, version)
	if err != nil {
		return nil, err
	}
	if schema > 1 {
		return nil, &NewerSchemaError{Path: path, Schema: schema}
	}
	if err := validator(upgraded); err != nil {
		return nil, fmt.Errorf("validate upgraded state %s: %w", path, err)
	}
	if schema == 1 {
		return nil, nil
	}
	backup, err := migrationBackupPath(projectRoot, path)
	if err != nil {
		return nil, err
	}
	return &upgradeCandidate{path: path, backup: backup, original: original, upgraded: upgraded, from: schema, to: 1, validator: validator}, nil
}

func backupReady(candidate upgradeCandidate) (bool, error) {
	data, err := readUpgradeSource(candidate.backup)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(data, candidate.original) {
		return false, errors.New("existing state migration backup differs from the source")
	}
	return true, nil
}

func UpgradeProject(ctx context.Context, projectValue project.Project, version string, apply bool) (UpgradeReport, error) {
	report := UpgradeReport{Schema: 1, ProjectID: projectValue.Marker.ProjectID, Apply: apply, Actions: make([]UpgradeAction, 0)}
	if version == "" || projectValue.Root == "" {
		return report, errors.New("state upgrade project or target version is empty")
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return report, err
	}
	defer projectLock.Release()

	if projectValue.Marker.Schema != project.MarkerSchema {
		action := UpgradeAction{Path: projectValue.MarkerPath, Backup: filepath.Join(projectValue.Root, "project.json"), FromSchema: projectValue.Marker.Schema, ToSchema: project.MarkerSchema}
		if apply {
			changed, upgradeErr := projectValue.UpgradeMarkers()
			if upgradeErr != nil {
				return report, upgradeErr
			}
			action.Applied = changed
		}
		report.Actions = append(report.Actions, action)
	}

	projectPath := filepath.Join(projectValue.Root, "resolved.json")
	var projectState ProjectState
	candidates := make([]upgradeCandidate, 0)
	projectCandidate, err := planCandidate(projectValue.Root, projectPath, version, projectValidator(projectValue.Marker.ProjectID, &projectState))
	if err != nil {
		return report, err
	}
	if projectCandidate != nil {
		candidates = append(candidates, *projectCandidate)
	}
	for _, definition := range projectState.Resolved.Nodes {
		nodeDir, err := projectValue.NodeDir(definition.Name)
		if err != nil {
			return report, err
		}
		nodePath := filepath.Join(nodeDir, "state.json")
		if _, err := os.Lstat(nodePath); errors.Is(err, os.ErrNotExist) {
			if projectCandidate != nil {
				return report, fmt.Errorf("legacy project state has no node state for %s", definition.Name)
			}
			continue
		}
		var nodeState NodeState
		candidate, err := planCandidate(projectValue.Root, nodePath, version, nodeValidator(projectValue.Marker.ProjectID, definition.Name, &nodeState))
		if err != nil {
			return report, err
		}
		if err := validateUpgradeStopped(nodeState); err != nil {
			return report, err
		}
		if candidate != nil {
			candidates = append(candidates, *candidate)
		}
		transactionPath := filepath.Join(nodeDir, "transaction.json")
		if _, err := os.Lstat(transactionPath); err == nil {
			candidate, err := planCandidate(projectValue.Root, transactionPath, version, transactionValidator(projectValue.Marker.ProjectID, definition.Name))
			if err != nil {
				return report, err
			}
			if candidate != nil {
				candidates = append(candidates, *candidate)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
	}

	backupExists := make([]bool, len(candidates))
	for index, candidate := range candidates {
		exists, err := backupReady(candidate)
		if err != nil {
			return report, err
		}
		backupExists[index] = exists
		report.Actions = append(report.Actions, UpgradeAction{Path: candidate.path, Backup: candidate.backup, FromSchema: candidate.from, ToSchema: candidate.to})
	}
	if !apply {
		return report, nil
	}
	for index, candidate := range candidates {
		if !backupExists[index] {
			if err := fsutil.AtomicWrite(candidate.backup, candidate.original, 0o600); err != nil {
				return report, err
			}
		}
	}
	for index, candidate := range candidates {
		current, err := readUpgradeSource(candidate.path)
		if err != nil || !bytes.Equal(current, candidate.original) {
			return report, fmt.Errorf("state changed after migration preflight: %s", candidate.path)
		}
		if err := fsutil.AtomicWrite(candidate.path, candidate.upgraded, 0o600); err != nil {
			return report, err
		}
		if err := candidate.validator(candidate.upgraded); err != nil {
			return report, err
		}
		report.Actions[index].Applied = true
	}
	return report, nil
}

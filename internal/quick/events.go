package quick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/piglet/internal/diagnostics"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/state"
)

var logPriorities = map[string]int{"error": 0, "warn": 1, "info": 2, "debug": 3}

func ValidLogLevel(level string) bool {
	_, ok := logPriorities[level]
	return ok
}

func (m Manager) qemuLogEnabled(level string) (bool, error) {
	configured := m.LogLevel
	if configured == "" {
		configured = "info"
	}
	configuredPriority, ok := logPriorities[configured]
	if !ok {
		return false, errors.New("invalid QEMU log level " + configured)
	}
	recordPriority, ok := logPriorities[level]
	if !ok {
		return false, errors.New("invalid QEMU record level " + level)
	}
	return recordPriority <= configuredPriority, nil
}

func (m Manager) operationID() (string, error) {
	if m.OperationID != "" {
		if !project.ValidUUID(m.OperationID) {
			return "", errors.New("operation ID must be a version-4 UUID")
		}
		return m.OperationID, nil
	}
	return project.NewUUID()
}

// RecordEvent appends to an existing stable node only. It never creates a node
// directory or state, which keeps plan/list/repair dry-runs non-mutating and
// prevents an event from changing orphan rollback ownership.
func (m Manager) RecordEvent(ctx context.Context, action, level, message string) error {
	operationID, err := m.operationID()
	if err != nil {
		return err
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return err
	}
	node, err := (state.Store{Project: projectValue}).ReadNode(nodeName)
	if err != nil {
		return err
	}
	nodeDir, err := projectValue.NodeDir(node.Node)
	if err != nil {
		return err
	}
	info, err := os.Lstat(nodeDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("event node directory is unsafe")
	}
	return diagnostics.AppendEvent(ctx, filepath.Join(nodeDir, "events.jsonl"), diagnostics.Event{
		Schema: 1, Time: time.Now().UTC(), Level: level,
		ProjectID: projectValue.Marker.ProjectID, Node: node.Node, OperationID: operationID,
		Action: action, Phase: string(node.Phase), Message: message,
	})
}

func (m Manager) recordQEMULog(ctx context.Context, projectValue project.Project, node state.NodeState, operationID, action, level, message string) error {
	enabled, err := m.qemuLogEnabled(level)
	if err != nil || !enabled {
		return err
	}
	nodeDir, err := projectValue.NodeDir(node.Node)
	if err != nil {
		return err
	}
	return diagnostics.AppendQEMULog(ctx, filepath.Join(nodeDir, "qemu.log"), diagnostics.QEMULogRecord{
		Schema: 1, Time: time.Now().UTC(), Level: level,
		ProjectID: projectValue.Marker.ProjectID, Node: node.Node, OperationID: operationID,
		Action: action, Message: message,
	})
}

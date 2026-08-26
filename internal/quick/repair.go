package quick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/state"
)

type RepairAction struct {
	Kind        string `json:"kind"`
	Path        string `json:"path,omitempty"`
	Reason      string `json:"reason"`
	Destructive bool   `json:"destructive"`
	Applied     bool   `json:"applied"`
}

type RepairReport struct {
	ProjectID   string         `json:"project_id"`
	OperationID string         `json:"operation_id"`
	Node        string         `json:"node"`
	Apply       bool           `json:"apply"`
	Blocked     bool           `json:"blocked"`
	Actions     []RepairAction `json:"actions"`
}

type RepairBlockedError struct{ Reason string }

func (e *RepairBlockedError) Error() string { return "repair blocked: " + e.Reason }

func (report *RepairReport) add(kind, path, reason string, destructive, applied bool) {
	report.Actions = append(report.Actions, RepairAction{Kind: kind, Path: path, Reason: reason, Destructive: destructive, Applied: applied})
}

func blockRepair(report *RepairReport, reason string) error {
	report.Blocked = true
	return &RepairBlockedError{Reason: reason}
}

func removeTransactionFile(nodeDir, reason string, report *RepairReport, apply bool) error {
	path := filepath.Join(nodeDir, "transaction.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return &RepairBlockedError{Reason: "transaction journal is not an owned regular file"}
	}
	if apply {
		if err := removeOwnedRegular(nodeDir, path); err != nil {
			return err
		}
	}
	report.add("remove-stale-journal", path, reason, true, apply)
	return nil
}

func (m Manager) repairStable(ctx context.Context, store state.Store, node state.NodeState, report *RepairReport, apply bool) error {
	if err := validateNodePaths(store.Project, node); err != nil {
		return blockRepair(report, err.Error())
	}
	if _, _, err := readConsistent(store, node.Node); err != nil {
		return blockRepair(report, err.Error())
	}
	life := lifecycle(m.runner())
	qmpErr := life.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID)
	stableAfterRepair := false
	if qmpErr == nil {
		identity, err := captureRuntimeIdentity(ctx, m.runner(), node)
		if err != nil {
			return blockRepair(report, "QMP identity matches but process identity cannot be reconstructed: "+err.Error())
		}
		stableAfterRepair = true
		if node.Phase != state.Running || node.Process != processToState(identity) {
			if apply {
				node.Phase = state.Running
				node.Process = processToState(identity)
				node.UpdatedAt = time.Now().UTC()
				if err := store.WriteNode(node); err != nil {
					return err
				}
			}
			report.add("update-state", filepath.Join(filepath.Dir(node.RootDisk), "state.json"), "QMP identity and the exact pidfile prove the VM is running", false, apply)
		}
	} else if process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
		return blockRepair(report, "recorded QEMU process is alive but QMP identity is unavailable")
	} else {
		switch node.Phase {
		case state.Running, state.Starting, state.Stopping:
			stableAfterRepair = true
			if apply {
				node.Phase = state.Stopped
				node.Process = state.ProcessIdentity{}
				node.UpdatedAt = time.Now().UTC()
				if err := store.WriteNode(node); err != nil {
					return err
				}
			}
			report.add("update-state", filepath.Join(filepath.Dir(node.RootDisk), "state.json"), "QMP and process identity prove the VM is not running", false, apply)
		case state.Prepared, state.Stopped:
			stableAfterRepair = true
		case state.Destroying:
			return blockRepair(report, "destroying state requires an explicit scoped destroy retry")
		default:
			return blockRepair(report, fmt.Sprintf("persisted node state has unsafe transitional phase %q", node.Phase))
		}
	}
	if stableAfterRepair {
		if err := removeTransactionFile(filepath.Dir(node.RootDisk), "node has a stable persisted state", report, apply); err != nil {
			var blocked *RepairBlockedError
			if errors.As(err, &blocked) {
				report.Blocked = true
			}
			return err
		}
	}
	return nil
}

func captureRuntimeIdentity(ctx context.Context, runner execx.Runner, node state.NodeState) (process.Identity, error) {
	info, err := os.Lstat(node.Runtime.PIDFile)
	if err != nil {
		return process.Identity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 32 {
		return process.Identity{}, errors.New("QEMU pidfile is not a small owned regular file")
	}
	data, err := os.ReadFile(node.Runtime.PIDFile)
	if err != nil {
		return process.Identity{}, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return process.Identity{}, errors.New("QEMU pidfile does not contain a positive PID")
	}
	identity, err := process.Capture(ctx, runner, node.Invocation, pid)
	if err != nil {
		return process.Identity{}, err
	}
	if !process.MatchesLive(ctx, runner, identity, node.Invocation) {
		return process.Identity{}, errors.New("captured QEMU process identity is not stable")
	}
	return identity, nil
}

func orphanRuntime(projectID string) (string, string, string, error) {
	directory, err := newRuntimeDirectory(projectID)
	if err != nil {
		return "", "", "", err
	}
	return directory, filepath.Join(directory, "qmp.sock"), filepath.Join(directory, "qemu.pid"), nil
}

func inspectOrphanRuntime(ctx context.Context, projectID string, report *RepairReport, apply bool) error {
	directory, qmpPath, pidPath, err := orphanRuntime(projectID)
	if err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return &RepairBlockedError{Reason: "orphan runtime directory is unsafe"}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "qmp.sock" && entry.Name() != "qemu.pid" {
			return &RepairBlockedError{Reason: "orphan runtime contains unexpected entry " + entry.Name()}
		}
	}
	if qmpInfo, err := os.Lstat(qmpPath); err == nil {
		if qmpInfo.Mode()&os.ModeSymlink != 0 || qmpInfo.Mode()&os.ModeSocket == 0 {
			return &RepairBlockedError{Reason: "orphan QMP path is not an owned socket"}
		}
		if _, err := (&qmp.Client{Timeout: time.Second}).QueryName(ctx, qmpPath); err == nil {
			return &RepairBlockedError{Reason: "orphan QMP socket responds; VM identity cannot be reconstructed safely"}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if pidInfo, err := os.Lstat(pidPath); err == nil {
		if !pidInfo.Mode().IsRegular() || pidInfo.Mode()&os.ModeSymlink != 0 || pidInfo.Size() > 32 {
			return &RepairBlockedError{Reason: "orphan pidfile is not a small owned regular file"}
		}
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return err
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil || pid <= 0 {
			return &RepairBlockedError{Reason: "orphan pidfile is malformed"}
		}
		if process.Alive(pid) {
			return &RepairBlockedError{Reason: fmt.Sprintf("orphan pid %d is alive", pid)}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, path := range []string{qmpPath, pidPath} {
		if _, err := os.Lstat(path); err == nil {
			if apply {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			report.add("remove-stale-runtime", path, "no QMP or live PID owns this exact runtime artifact", true, apply)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if apply {
		if err := os.Remove(directory); err != nil {
			return err
		}
	}
	report.add("remove-empty-runtime-dir", directory, "runtime contains no live or unexpected resources", true, apply)
	return nil
}

func requireOwnedDirectory(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return &RepairBlockedError{Reason: label + " is not an owned mode-0700 directory"}
	}
	return nil
}

func (m Manager) repairOrphan(ctx context.Context, store state.Store, transaction state.Transaction, report *RepairReport, apply bool) error {
	if transaction.From != state.Absent || (transaction.To != state.Preparing && transaction.To != state.Prepared) {
		return blockRepair(report, "orphan journal is not an absent-to-prepare transaction")
	}
	if transaction.Node != nodeName {
		return blockRepair(report, fmt.Sprintf("orphan journal node %q does not match %q", transaction.Node, nodeName))
	}
	nodeDir, err := store.Project.NodeDir(transaction.Node)
	if err != nil {
		return err
	}
	if err := requireOwnedDirectory(filepath.Dir(nodeDir), "nodes directory"); err != nil {
		var blocked *RepairBlockedError
		if errors.As(err, &blocked) {
			report.Blocked = true
		}
		return err
	}
	if err := requireOwnedDirectory(nodeDir, "node directory"); err != nil {
		var blocked *RepairBlockedError
		if errors.As(err, &blocked) {
			report.Blocked = true
		}
		return err
	}
	runtimeDir, qmpPath, _, err := orphanRuntime(store.Project.Marker.ProjectID)
	if err != nil {
		return err
	}
	expected := map[string]string{
		"project-key":  filepath.Join(store.Project.Root, "keys"),
		"root-overlay": filepath.Join(nodeDir, "root.qcow2"),
		"data-disk":    filepath.Join(nodeDir, "data.qcow2"),
		"seed":         filepath.Join(nodeDir, "seed.iso"),
		"nvram":        filepath.Join(nodeDir, "nvram.fd"),
		"invocation":   qmpPath,
	}
	rollbackPaths := make([]string, 0, len(transaction.Completed))
	rollbackNames := make(map[string]struct{}, len(transaction.Completed))
	seenActions := make(map[string]struct{}, len(transaction.Completed))
	for index := len(transaction.Completed) - 1; index >= 0; index-- {
		action := transaction.Completed[index]
		expectedPath, known := expected[action.Name]
		if !known || action.Resource != expectedPath {
			report.add("preserve-unowned", action.Resource, "journal action does not match one exact owned resource", false, false)
			return blockRepair(report, fmt.Sprintf("cannot prove ownership for journal action %q", action.Name))
		}
		key := action.Name + "\x00" + action.Resource
		if _, duplicate := seenActions[key]; duplicate {
			continue
		}
		seenActions[key] = struct{}{}
		switch action.Name {
		case "project-key":
			report.add("preserve-project-key", action.Resource, "project keys predate node readiness and are retained by policy", false, false)
			continue
		case "invocation":
			if filepath.Dir(action.Resource) != runtimeDir {
				return blockRepair(report, "invocation runtime path does not match project identity")
			}
			continue
		}
		info, statErr := os.Lstat(action.Resource)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return blockRepair(report, "transaction-owned resource has an unexpected file type: "+action.Resource)
		}
		rollbackPaths = append(rollbackPaths, action.Resource)
		rollbackNames[filepath.Base(action.Resource)] = struct{}{}
	}
	entries, err := os.ReadDir(nodeDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "transaction.json" {
			continue
		}
		if _, owned := rollbackNames[entry.Name()]; !owned {
			return blockRepair(report, "node directory contains resource not owned by rollback: "+entry.Name())
		}
	}
	preflight := RepairReport{}
	if err := inspectOrphanRuntime(ctx, store.Project.Marker.ProjectID, &preflight, false); err != nil {
		report.Blocked = true
		return err
	}
	if err := inspectOrphanRuntime(ctx, store.Project.Marker.ProjectID, report, apply); err != nil {
		report.Blocked = true
		return err
	}
	for _, path := range rollbackPaths {
		if apply {
			if err := removeOwnedRegular(nodeDir, path); err != nil {
				return err
			}
		}
		report.add("rollback-created-resource", path, "resource was created by the interrupted absent-to-prepare transaction", true, apply)
	}
	if err := removeTransactionFile(nodeDir, "interrupted prepare rollback passed ownership and runtime preflight", report, apply); err != nil {
		var blocked *RepairBlockedError
		if errors.As(err, &blocked) {
			report.Blocked = true
		}
		return err
	}
	if apply {
		entries, err = os.ReadDir(nodeDir)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return blockRepair(report, "node directory changed during rollback; unexpected resources were preserved")
		}
		if err := os.Remove(nodeDir); err != nil {
			return err
		}
		report.add("remove-empty-node-dir", nodeDir, "all transaction-owned resources were removed", true, true)
	} else {
		report.add("remove-empty-node-dir", nodeDir, "would remove only after all transaction-owned resources are gone", true, false)
	}
	return nil
}

func (m Manager) Repair(ctx context.Context, apply bool) (RepairReport, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return RepairReport{}, err
	}
	operationID, err := m.operationID()
	if err != nil {
		return RepairReport{}, err
	}
	report := RepairReport{ProjectID: projectValue.Marker.ProjectID, OperationID: operationID, Node: nodeName, Apply: apply}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return report, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	node, nodeErr := store.ReadNode(nodeName)
	if nodeErr == nil {
		transaction, transactionErr := store.ReadTransaction(nodeName)
		if transactionErr == nil && transaction.Reconcile != nil {
			return report, m.repairReconcile(ctx, store, node, transaction, &report, apply)
		}
		if transactionErr != nil && !errors.Is(transactionErr, os.ErrNotExist) {
			return report, blockRepair(&report, transactionErr.Error())
		}
		return report, m.repairStable(ctx, store, node, &report, apply)
	}
	if !errors.Is(nodeErr, os.ErrNotExist) {
		return report, nodeErr
	}
	transaction, transactionErr := store.ReadTransaction(nodeName)
	if errors.Is(transactionErr, os.ErrNotExist) {
		return report, nil
	}
	if transactionErr != nil {
		return report, transactionErr
	}
	err = m.repairOrphan(ctx, store, transaction, &report, apply)
	if _, blocked := err.(*RepairBlockedError); blocked {
		report.Blocked = true
	}
	return report, err
}

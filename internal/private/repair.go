package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

type RepairAction struct {
	Node    string `json:"node,omitempty"`
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Reason  string `json:"reason"`
	Applied bool   `json:"applied"`
}

type RepairReport struct {
	ProjectID string         `json:"project_id"`
	Apply     bool           `json:"apply"`
	Blocked   bool           `json:"blocked"`
	Actions   []RepairAction `json:"actions"`
}

type RepairBlockedError struct{ Reason string }

func (e *RepairBlockedError) Error() string { return "private repair blocked: " + e.Reason }

func privatePID(path string) (int, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 32 {
		return 0, errors.New("private pidfile is missing or unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, errors.New("private pidfile does not contain a positive PID")
	}
	return pid, nil
}

func captureNodeIdentity(ctx context.Context, manager Manager, node state.NodeState) (process.Identity, error) {
	pid, err := privatePID(node.Runtime.PIDFile)
	if err != nil {
		return process.Identity{}, err
	}
	identityValue, err := process.Capture(ctx, manager.runner(), node.Invocation, pid)
	if err != nil || !process.MatchesLive(ctx, manager.runner(), identityValue, node.Invocation) {
		return process.Identity{}, errors.New("private QEMU process identity cannot be reconstructed")
	}
	return identityValue, nil
}

func inspectRuntimeForCleanup(node state.NodeState) (bool, error) {
	info, err := os.Lstat(node.Runtime.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return false, errors.New("private runtime directory is unsafe")
	}
	entries, err := os.ReadDir(node.Runtime.Directory)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() != "qmp.sock" && entry.Name() != "qemu.pid" {
			return false, fmt.Errorf("private runtime has unexpected entry %s", entry.Name())
		}
		path := filepath.Join(node.Runtime.Directory, entry.Name())
		entryInfo, err := os.Lstat(path)
		if err != nil || entryInfo.Mode()&os.ModeSymlink != 0 || entryInfo.IsDir() {
			return false, fmt.Errorf("private runtime artifact is unsafe: %s", path)
		}
	}
	return true, nil
}

func (m Manager) Repair(ctx context.Context, apply bool) (RepairReport, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return RepairReport{}, err
	}
	report := RepairReport{ProjectID: projectValue.Marker.ProjectID, Apply: apply, Actions: []RepairAction{}}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return report, err
	}
	defer projectLock.Release()
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return report, errors.New("current project has no valid private state")
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return report, err
	}
	selectedSet := nodeNameSet(selected)
	lifecycle := vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: time.Second}, SSHUser: projectState.Resolved.SSHUser}
	nodes := make([]state.NodeState, 0, len(selected))
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			report.Blocked = true
			return report, &RepairBlockedError{Reason: fmt.Sprintf("node %s has no stable state: %v", definition.Name, err)}
		}
		qmpErr := lifecycle.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID)
		if qmpErr == nil {
			identityValue, err := captureNodeIdentity(ctx, m, node)
			if err != nil {
				report.Blocked = true
				return report, &RepairBlockedError{Reason: fmt.Sprintf("node %s QMP is live but process identity failed: %v", node.Node, err)}
			}
			desired := state.ProcessIdentity{PID: identityValue.PID, Executable: identityValue.Executable, Started: identityValue.Started, ArgvHash: identityValue.ArgvHash}
			if node.Phase != state.Running || node.Process != desired {
				report.Actions = append(report.Actions, RepairAction{Node: node.Node, Kind: "update-state-running", Path: filepath.Join(filepath.Dir(node.RootDisk), "state.json"), Reason: "matching QMP name/UUID and process argv prove the VM is running", Applied: apply})
				if apply {
					node.Phase = state.Running
					node.Process = desired
					node.UpdatedAt = time.Now().UTC()
					if err := store.WriteNode(node); err != nil {
						return report, err
					}
				}
			}
			nodes = append(nodes, node)
			continue
		}
		persistedIdentity := process.Identity{PID: node.Process.PID, Executable: node.Process.Executable, Started: node.Process.Started, ArgvHash: node.Process.ArgvHash}
		if process.MatchesLive(ctx, m.runner(), persistedIdentity, node.Invocation) || process.Alive(node.Process.PID) {
			report.Blocked = true
			return report, &RepairBlockedError{Reason: fmt.Sprintf("node %s has a live process without matching QMP identity", node.Node)}
		}
		switch node.Phase {
		case state.Running, state.Starting, state.Stopping:
			report.Actions = append(report.Actions, RepairAction{Node: node.Node, Kind: "update-state-stopped", Path: filepath.Join(filepath.Dir(node.RootDisk), "state.json"), Reason: "QMP and process identity prove the VM is dead", Applied: apply})
			if apply {
				node.Phase = state.Stopped
				node.Process = state.ProcessIdentity{}
				node.UpdatedAt = time.Now().UTC()
				if err := store.WriteNode(node); err != nil {
					return report, err
				}
			}
		case state.Prepared, state.Stopped:
		case state.Destroying:
			report.Blocked = true
			return report, &RepairBlockedError{Reason: fmt.Sprintf("node %s is destroying; retry scoped destroy", node.Node)}
		default:
			report.Blocked = true
			return report, &RepairBlockedError{Reason: fmt.Sprintf("node %s phase %s is unsupported", node.Node, node.Phase)}
		}
		cleanup, err := inspectRuntimeForCleanup(node)
		if err != nil {
			report.Blocked = true
			return report, &RepairBlockedError{Reason: err.Error()}
		}
		if cleanup {
			report.Actions = append(report.Actions, RepairAction{Node: node.Node, Kind: "remove-stale-runtime", Path: node.Runtime.Directory, Reason: "runtime has no matching QMP or live process", Applied: apply})
			if apply {
				if err := cleanupRuntime(node); err != nil {
					return report, err
				}
			}
		}
		nodes = append(nodes, node)
	}
	leaseStatus, err := m.leaseStore().Inspect()
	if err != nil {
		return report, err
	}
	if leaseStatus.Active {
		if leaseStatus.Lease.ProjectID != projectValue.Marker.ProjectID || leaseStatus.Lease.OwnerUID != os.Getuid() {
			report.Blocked = true
			return report, &RepairBlockedError{Reason: "host-global lease belongs to another project or UID"}
		}
		desired, err := SynchronizeLease(*leaseStatus.Lease, nodes)
		if err != nil {
			return report, err
		}
		report.Actions = append(report.Actions, RepairAction{Kind: "synchronize-lease", Path: filepath.Join(leaseStatus.Root, "private-lease.json"), Reason: "mirror repaired node phases and process identities", Applied: apply})
		if apply {
			updated, err := m.leaseStore().Update(ctx, desired)
			if err != nil {
				return report, err
			}
			allStopped := true
			for _, node := range updated.Lease.Nodes {
				allStopped = allStopped && node.Phase == lease.Stopped
			}
			if allStopped {
				if _, err := m.leaseStore().Release(ctx, projectValue.Marker.ProjectID, true, lease.RuntimeIdentityAuditor(m.runner(), time.Second)); err != nil {
					return report, err
				}
				report.Actions = append(report.Actions, RepairAction{Kind: "release-dead-lease", Path: filepath.Join(leaseStatus.Root, "private-lease.json"), Reason: "every repaired node is stopped and runtime audit is dead", Applied: true})
			}
			_ = updated
		}
	}
	return report, nil
}

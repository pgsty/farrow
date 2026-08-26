package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/farrow/internal/hostconfig"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

func integrationHome(home string) (string, error) {
	if home != "" {
		return filepath.Abs(home)
	}
	return os.UserHomeDir()
}

func (m Manager) integrationSnapshot(ctx context.Context) (project.Project, state.ProjectState, []state.NodeState, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return project.Project{}, state.ProjectState{}, nil, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), true)
	if err != nil {
		return project.Project{}, state.ProjectState{}, nil, err
	}
	defer projectLock.Release()
	return m.integrationSnapshotLocked(projectValue)
}

func (m Manager) integrationSnapshotLocked(projectValue project.Project) (project.Project, state.ProjectState, []state.NodeState, error) {
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil {
		return project.Project{}, state.ProjectState{}, nil, err
	}
	if projectState.Resolved.Network != "private" {
		return project.Project{}, state.ProjectState{}, nil, errors.New("current project is not private")
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return project.Project{}, state.ProjectState{}, nil, err
	}
	selectedSet := nodeNameSet(selected)
	filtered := cloneResolved(projectState.Resolved)
	filtered.Nodes = filtered.Nodes[:0]
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; include {
			filtered.Nodes = append(filtered.Nodes, definition)
		}
	}
	projectState.Resolved = filtered
	nodes := make([]state.NodeState, 0, len(filtered.Nodes))
	for _, definition := range filtered.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return project.Project{}, state.ProjectState{}, nil, err
		}
		if node.SpecHash != projectState.SpecHash {
			return project.Project{}, state.ProjectState{}, nil, fmt.Errorf("private node %s and project spec hashes differ", definition.Name)
		}
		if node.SSHPort == 0 || node.Phase == state.Absent {
			return project.Project{}, state.ProjectState{}, nil, fmt.Errorf("private node %s has no installable SSH endpoint", definition.Name)
		}
		nodes = append(nodes, node)
	}
	return projectValue, projectState, nodes, nil
}

func validateSSHArtifacts(projectValue project.Project) (string, string, error) {
	return project.ValidateSSHArtifacts(projectValue)
}

// Connections resolves and verifies a selected batch while holding the
// project exclusive lock across the complete snapshot and runtime checks.
func (m Manager) Connections(ctx context.Context) ([]Connection, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return nil, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return nil, err
	}
	defer projectLock.Release()
	projectValue, err = m.openProject(false)
	if err != nil {
		return nil, err
	}
	return m.ConnectionsLocked(ctx, projectValue, projectLock)
}

// ConnectionsLocked is the non-locking provisioning entrypoint. The caller
// must pass the live exclusive project.lock token and keep it held for the
// complete remote operation, preventing lifecycle changes after validation.
func (m Manager) ConnectionsLocked(ctx context.Context, projectValue project.Project, projectLock *lock.File) ([]Connection, error) {
	if err := projectLock.ValidateExclusive(filepath.Join(projectValue.Root, "project.lock")); err != nil {
		return nil, fmt.Errorf("private connections require the matching exclusive project lock: %w", err)
	}
	refreshed, err := project.Open(projectValue.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("re-open private project under its exclusive lock: %w", err)
	}
	if err := projectLock.ValidateExclusive(filepath.Join(refreshed.Root, "project.lock")); err != nil {
		return nil, fmt.Errorf("private project marker changed while locked: %w", err)
	}
	projectValue = refreshed
	_, projectState, nodes, err := m.integrationSnapshotLocked(projectValue)
	if err != nil {
		return nil, err
	}
	privateKey, knownHosts, err := validateSSHArtifacts(projectValue)
	if err != nil {
		return nil, err
	}
	lifecycle := vm.Lifecycle{Runner: m.runner(), QMP: &qmp.Client{Timeout: 5 * time.Second}, SSHUser: projectState.Resolved.SSHUser}
	connections := make([]Connection, 0, len(nodes))
	for index, node := range nodes {
		definition := projectState.Resolved.Nodes[index]
		if node.Phase != state.Running {
			return nil, fmt.Errorf("private node %s is not running", node.Node)
		}
		identity := process.Identity{PID: node.Process.PID, Executable: node.Process.Executable, Started: node.Process.Started, ArgvHash: node.Process.ArgvHash}
		if err := lifecycle.ValidateIdentity(ctx, node.Runtime.QMP, node.Node, node.VMUUID); err != nil {
			return nil, fmt.Errorf("private node %s QMP identity does not match: %w", node.Node, err)
		}
		if !process.MatchesLive(ctx, m.runner(), identity, node.Invocation) {
			return nil, fmt.Errorf("private node %s recorded process identity does not match", node.Node)
		}
		connections = append(connections, Connection{
			Node: definition.Name, User: projectState.Resolved.SSHUser,
			Host: "127.0.0.1", Port: node.SSHPort,
			PrivateKey: privateKey, KnownHosts: knownHosts,
		})
	}
	return connections, nil
}

// InstallSSHConfig writes all persisted private nodes into one marker-owned
// fragment. Each node is addressable by the explicit fragment prefix, its
// node name, and its fixed private address.
func (m Manager) InstallSSHConfig(ctx context.Context, name, home string) (sshconfig.Result, error) {
	home, err := integrationHome(home)
	if err != nil {
		return sshconfig.Result{}, err
	}
	projectValue, projectState, nodes, err := m.integrationSnapshot(ctx)
	if err != nil {
		return sshconfig.Result{}, err
	}
	identity, knownHosts, err := validateSSHArtifacts(projectValue)
	if err != nil {
		return sshconfig.Result{}, err
	}
	entries := make([]sshconfig.Entry, 0, len(nodes))
	for index, definition := range projectState.Resolved.Nodes {
		aliases := []string{definition.Name}
		if definition.Address != "" {
			aliases = append(aliases, definition.Address)
		}
		aliases = append(aliases, definition.Aliases...)
		entries = append(entries, sshconfig.Entry{
			ProjectID:  projectValue.Marker.ProjectID,
			Name:       name,
			Node:       definition.Name,
			Aliases:    aliases,
			User:       projectState.Resolved.SSHUser,
			Host:       "127.0.0.1",
			Port:       nodes[index].SSHPort,
			Identity:   identity,
			KnownHosts: knownHosts,
		})
	}
	return sshconfig.InstallMany(home, entries)
}

func (m Manager) RemoveSSHConfig(name, home string) (sshconfig.Result, error) {
	home, err := integrationHome(home)
	if err != nil {
		return sshconfig.Result{}, err
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return sshconfig.Result{}, err
	}
	return sshconfig.Remove(home, projectValue.Marker.ProjectID, name)
}

// HostEntries returns the fixed private address, node name, and declared
// profile aliases for a marker-owned /etc/hosts block.
func (m Manager) HostEntries(ctx context.Context) (string, []hostconfig.Entry, error) {
	projectValue, projectState, _, err := m.integrationSnapshot(ctx)
	if err != nil {
		return "", nil, err
	}
	entries := make([]hostconfig.Entry, 0, len(projectState.Resolved.Nodes))
	for _, definition := range projectState.Resolved.Nodes {
		names := make([]string, 0, len(definition.Aliases)+1)
		seen := make(map[string]struct{}, len(definition.Aliases)+1)
		for _, name := range append([]string{definition.Name}, definition.Aliases...) {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		entries = append(entries, hostconfig.Entry{Address: definition.Address, Names: names})
	}
	return projectValue.Marker.ProjectID, entries, nil
}

func (m Manager) ProjectID() (string, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return "", err
	}
	return projectValue.Marker.ProjectID, nil
}

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
	"github.com/pgsty/farrow/internal/qmp"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/pgsty/farrow/internal/sshkeys"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
)

func integrationHome(home string) (string, error) {
	if home != "" {
		return filepath.Abs(home)
	}
	return os.UserHomeDir()
}

func (m Manager) integrationSnapshot(ctx context.Context) (Deployment, state.DeploymentState, []state.NodeState, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Deployment{}, state.DeploymentState{}, nil, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := acquireDeploymentLock(lockContext, projectValue.Root, true)
	if err != nil {
		return Deployment{}, state.DeploymentState{}, nil, err
	}
	defer projectLock.Release()
	return m.integrationSnapshotLocked(projectValue)
}

func (m Manager) integrationSnapshotLocked(projectValue Deployment) (Deployment, state.DeploymentState, []state.NodeState, error) {
	store := state.Store{Root: projectValue.Root}
	projectState, err := store.ReadDeployment()
	if err != nil {
		return Deployment{}, state.DeploymentState{}, nil, err
	}
	if projectState.Resolved.Network != "private" {
		return Deployment{}, state.DeploymentState{}, nil, errors.New("the deployment state is not private")
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Deployment{}, state.DeploymentState{}, nil, err
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
			return Deployment{}, state.DeploymentState{}, nil, err
		}
		expectedHash, hashErr := spec.NodeHash(projectState.Resolved, definition.Name)
		if hashErr != nil || node.SpecHash != expectedHash {
			return Deployment{}, state.DeploymentState{}, nil, fmt.Errorf("private node %s state does not match its resolved node hash", definition.Name)
		}
		if node.SSHPort == 0 || node.Phase == state.Absent {
			return Deployment{}, state.DeploymentState{}, nil, fmt.Errorf("private node %s has no installable SSH endpoint", definition.Name)
		}
		nodes = append(nodes, node)
	}
	return projectValue, projectState, nodes, nil
}

func validateSSHArtifacts(projectValue Deployment) (string, string, error) {
	return sshkeys.ValidateSSHArtifacts(projectValue.Root)
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
	projectLock, err := acquireDeploymentLock(lockContext, projectValue.Root, false)
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
func (m Manager) ConnectionsLocked(ctx context.Context, projectValue Deployment, projectLock *lock.File) ([]Connection, error) {
	if err := projectLock.ValidateExclusive(deploymentLockPath(projectValue.Root)); err != nil {
		return nil, fmt.Errorf("private connections require the matching exclusive deployment lock: %w", err)
	}
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
	identityFile, knownHosts, err := validateSSHArtifacts(projectValue)
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
			Name:       name,
			Node:       definition.Name,
			Aliases:    aliases,
			User:       projectState.Resolved.SSHUser,
			Host:       "127.0.0.1",
			Port:       nodes[index].SSHPort,
			Identity:   identityFile,
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
	return sshconfig.Remove(home, name)
}

// HostEntries returns the fixed private address, node name, and declared
// profile aliases for the marker-owned /etc/hosts block.
func (m Manager) HostEntries(ctx context.Context) ([]hostconfig.Entry, error) {
	_, projectState, _, err := m.integrationSnapshot(ctx)
	if err != nil {
		return nil, err
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
	return entries, nil
}

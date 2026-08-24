package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pgsty/piglet/internal/hostconfig"
	"github.com/pgsty/piglet/internal/lock"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/sshconfig"
	"github.com/pgsty/piglet/internal/state"
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
		if node.SSHPort == 0 || node.Phase == state.Absent {
			return project.Project{}, state.ProjectState{}, nil, fmt.Errorf("private node %s has no installable SSH endpoint", definition.Name)
		}
		nodes = append(nodes, node)
	}
	return projectValue, projectState, nodes, nil
}

func validateSSHArtifacts(projectValue project.Project) (string, string, error) {
	keysDir := filepath.Join(projectValue.Root, "keys")
	keysInfo, err := os.Lstat(keysDir)
	if err != nil || !keysInfo.IsDir() || keysInfo.Mode()&os.ModeSymlink != 0 || keysInfo.Mode().Perm() != 0o700 {
		return "", "", errors.New("private SSH keys directory is missing or unsafe")
	}
	keysStat, ok := keysInfo.Sys().(*syscall.Stat_t)
	if !ok || int(keysStat.Uid) != os.Geteuid() {
		return "", "", errors.New("private SSH keys directory ownership is unsafe")
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(projectValue.Root)
	canonicalKeys, keysErr := filepath.EvalSymlinks(keysDir)
	if rootErr != nil || keysErr != nil || canonicalKeys != filepath.Join(canonicalRoot, "keys") {
		return "", "", errors.New("private SSH keys directory escapes the project root")
	}
	identity := filepath.Join(keysDir, "id_ed25519")
	knownHosts := filepath.Join(keysDir, "known_hosts")
	for pathname, mode := range map[string]os.FileMode{identity: 0o600, knownHosts: 0o600} {
		info, err := os.Lstat(pathname)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
			return "", "", fmt.Errorf("private SSH artifact is missing or unsafe: %s", pathname)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 {
			return "", "", fmt.Errorf("private SSH artifact ownership or link count is unsafe: %s", pathname)
		}
		handle, err := os.Open(pathname)
		if err != nil {
			return "", "", err
		}
		opened, statErr := handle.Stat()
		closeErr := handle.Close()
		if statErr != nil || closeErr != nil || !os.SameFile(info, opened) {
			return "", "", fmt.Errorf("private SSH artifact identity changed while opening: %s", pathname)
		}
	}
	return identity, knownHosts, nil
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

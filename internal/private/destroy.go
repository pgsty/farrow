package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/lease"
	"github.com/pgsty/piglet/internal/lock"
	"github.com/pgsty/piglet/internal/persistent"
	"github.com/pgsty/piglet/internal/process"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

func ownedRegularWithin(root, path string) error {
	inside, err := fsutil.IsWithin(root, path)
	if err != nil || !inside {
		return fmt.Errorf("private destroy target escapes node root: %s", path)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private destroy target is unsafe: %s", path)
	}
	return nil
}

func removeOwnedRegularWithin(root, path string) error {
	if err := ownedRegularWithin(root, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fsutil.SyncDir(root)
}

func privateNodeTargets(projectValue project.Project, node state.NodeState) (string, []string, error) {
	nodeDir, err := projectValue.NodeDir(node.Node)
	if err != nil {
		return "", nil, err
	}
	expected := map[string]struct{}{
		filepath.Join(nodeDir, "state.json"): {}, node.RootDisk: {}, node.Seed: {},
		filepath.Join(nodeDir, "serial.log"): {}, filepath.Join(nodeDir, "qemu.log"): {}, filepath.Join(nodeDir, "events.jsonl"): {},
	}
	if node.NVRAM != "" {
		expected[node.NVRAM] = struct{}{}
	}
	for _, disk := range node.DataDisks {
		inside, err := fsutil.IsWithin(nodeDir, disk.Path)
		if err != nil {
			return "", nil, err
		}
		if inside {
			expected[disk.Path] = struct{}{}
		} else if !disk.Persistent {
			return "", nil, fmt.Errorf("non-persistent private disk escapes node root: %s", disk.Path)
		}
	}
	entries, err := os.ReadDir(nodeDir)
	if err != nil {
		return "", nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(nodeDir, entry.Name())
		if _, ok := expected[path]; !ok {
			return "", nil, fmt.Errorf("refuse private destroy with unexpected node artifact %s", path)
		}
		if err := ownedRegularWithin(nodeDir, path); err != nil {
			return "", nil, err
		}
	}
	ordered := make([]string, 0, len(expected))
	for _, path := range []string{node.RootDisk} {
		if path != "" {
			ordered = append(ordered, path)
		}
	}
	for _, disk := range node.DataDisks {
		if !disk.Persistent {
			ordered = append(ordered, disk.Path)
		}
	}
	for _, path := range []string{node.Seed, node.NVRAM, filepath.Join(nodeDir, "serial.log"), filepath.Join(nodeDir, "qemu.log"), filepath.Join(nodeDir, "events.jsonl"), filepath.Join(nodeDir, "state.json")} {
		if path != "" {
			ordered = append(ordered, path)
		}
	}
	return nodeDir, ordered, nil
}

func (m Manager) removeKnownHostEntries(ctx context.Context, projectValue project.Project, nodes []state.NodeState, resolvedAddresses map[string]string) error {
	sshKeygen, err := m.lookPath("ssh-keygen")
	if err != nil {
		return err
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	knownHosts := filepath.Join(keysDir, "known_hosts")
	for _, node := range nodes {
		for _, host := range []string{fmt.Sprintf("[127.0.0.1]:%d", node.SSHPort), resolvedAddresses[node.Node]} {
			if host == "" {
				continue
			}
			if _, err := m.runner().Run(ctx, sshKeygen, "-f", knownHosts, "-R", host); err != nil {
				return err
			}
			backup := knownHosts + ".old"
			if info, err := os.Lstat(backup); err == nil {
				if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					return errors.New("ssh-keygen created an unsafe private known_hosts backup")
				}
				if err := os.Remove(backup); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return fsutil.SyncDir(keysDir)
}

func (m Manager) Destroy(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project has no valid private state")
	}
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	partial := len(selected) != len(projectState.Resolved.Nodes)
	if partial && !m.allowPartialDestroy {
		return Status{}, errors.New("private destroy currently requires selecting the complete project")
	}
	selectedSet := nodeNameSet(selected)
	needsStop := false
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		needsStop = needsStop || node.Phase == state.Running || node.Phase == state.Prepared
	}
	if needsStop {
		if _, err := m.Stop(ctx); err != nil {
			return Status{}, err
		}
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(projectValue.Root, "project.lock"), false)
	if err != nil {
		return Status{}, err
	}
	defer projectLock.Release()
	leaseStatus, err := m.leaseStore().Inspect()
	if err != nil {
		return Status{}, err
	}
	if leaseStatus.Active {
		if !partial {
			return Status{}, errors.New("refuse private destroy while a host-global lease is active")
		}
		if leaseStatus.Lease.ProjectID != projectValue.Marker.ProjectID || leaseStatus.Lease.OwnerUID != os.Getuid() {
			return Status{}, errors.New("refuse partial recreate while another project or UID owns the private lease")
		}
		for _, leased := range leaseStatus.Lease.Nodes {
			if _, include := selectedSet[leased.Name]; include && leased.Phase != lease.Stopped {
				return Status{}, fmt.Errorf("refuse partial recreate while selected lease node %s phase is %s", leased.Name, leased.Phase)
			}
		}
	}
	nodes := make([]state.NodeState, 0, len(projectState.Resolved.Nodes))
	allNodes := make([]state.NodeState, 0, len(projectState.Resolved.Nodes))
	targets := make(map[string][]string)
	nodeDirs := make(map[string]string)
	addresses := make(map[string]string)
	for _, definition := range projectState.Resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil {
			return Status{}, err
		}
		allNodes = append(allNodes, node)
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		if node.Phase != state.Stopped && node.Phase != state.Prepared {
			return Status{}, fmt.Errorf("private node %s phase %s is not destroyable", node.Node, node.Phase)
		}
		identityValue := process.Identity{PID: node.Process.PID, Executable: node.Process.Executable, Started: node.Process.Started, ArgvHash: node.Process.ArgvHash}
		if process.MatchesLive(ctx, m.runner(), identityValue, node.Invocation) || process.Alive(node.Process.PID) {
			return Status{}, fmt.Errorf("refuse private destroy while node %s process is live", node.Node)
		}
		nodeDir, nodeTargets, err := privateNodeTargets(projectValue, node)
		if err != nil {
			return Status{}, err
		}
		nodes = append(nodes, node)
		targets[node.Node] = nodeTargets
		nodeDirs[node.Node] = nodeDir
		addresses[node.Node] = definition.Address
	}
	persistentIdentities, err := validatePrivatePersistentState(projectValue, projectState, allNodes)
	if err != nil {
		return Status{}, err
	}
	resolvedPath := filepath.Join(projectValue.Root, "resolved.json")
	if !partial {
		if err := ownedRegularWithin(projectValue.Root, resolvedPath); err != nil {
			return Status{}, err
		}
	}
	for index := range nodes {
		nodes[index].Phase = state.Destroying
		nodes[index].UpdatedAt = time.Now().UTC()
		if err := store.WriteNode(nodes[index]); err != nil {
			return Status{}, err
		}
	}
	for _, node := range nodes {
		for _, identityValue := range persistentIdentities {
			if identityValue.Node != node.Node {
				continue
			}
			source := ""
			for _, dataDisk := range node.DataDisks {
				if dataDisk.Name == identityValue.Name {
					source = dataDisk.Path
					break
				}
			}
			if source == "" {
				return Status{}, fmt.Errorf("persistent private disk %s/%s has no state path", identityValue.Node, identityValue.Name)
			}
			if _, err := persistent.Preserve(projectValue, identityValue, source); err != nil {
				return Status{}, err
			}
		}
		for _, path := range targets[node.Node] {
			if err := removeOwnedRegularWithin(nodeDirs[node.Node], path); err != nil {
				return Status{}, err
			}
		}
		if err := cleanupRuntime(node); err != nil {
			return Status{}, err
		}
		if err := os.Remove(nodeDirs[node.Node]); err != nil {
			return Status{}, fmt.Errorf("private node directory contains unexpected artifacts: %w", err)
		}
	}
	if err := m.removeKnownHostEntries(ctx, projectValue, nodes, addresses); err != nil {
		return Status{}, err
	}
	if !partial {
		if err := removeOwnedRegularWithin(projectValue.Root, resolvedPath); err != nil {
			return Status{}, err
		}
	}
	message := "destroyed private node artifacts; image cache, project marker, keys, and persistent data disks preserved"
	if partial {
		message = "destroyed selected private node artifacts for immediate recreate; project state, peer nodes, lease, keys, and persistent data disks preserved"
	}
	result := Status{ProjectID: projectValue.Marker.ProjectID, OperationID: m.OperationID, SpecHash: projectState.SpecHash, Message: message}
	for _, definition := range projectState.Resolved.Nodes {
		if _, include := selectedSet[definition.Name]; !include {
			continue
		}
		result.Nodes = append(result.Nodes, NodeStatus{Name: definition.Name, Address: definition.Address, State: state.Absent, Runtime: "inactive"})
	}
	return result, nil
}

func (m Manager) Recreate(ctx context.Context) (Status, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project has no valid private state")
	}
	return m.RecreateResolved(ctx, projectState.Resolved)
}

func validatePrivateRecreatePersistent(projectValue project.Project, current, requested spec.Resolved) error {
	currentIdentities, err := privatePersistentIdentities(projectValue, current)
	if err != nil {
		return err
	}
	requestedIdentities, err := privatePersistentIdentities(projectValue, requested)
	if err != nil {
		return err
	}
	if len(currentIdentities) != len(requestedIdentities) {
		return errors.New("recreate desired configuration changes the persistent disk set")
	}
	wanted := make(map[string]persistent.Identity, len(requestedIdentities))
	for _, identityValue := range requestedIdentities {
		wanted[identityValue.Node+"\x00"+identityValue.Name] = identityValue
	}
	for _, identityValue := range currentIdentities {
		candidate, ok := wanted[identityValue.Node+"\x00"+identityValue.Name]
		if !ok || candidate.Serial != identityValue.Serial || candidate.Size != identityValue.Size || candidate.Mount != identityValue.Mount || candidate.Filesystem != identityValue.Filesystem {
			return fmt.Errorf("recreate desired configuration is incompatible with persistent disk %s/%s", identityValue.Node, identityValue.Name)
		}
	}
	_, err = persistent.ValidateDesired(projectValue, requestedIdentities)
	return err
}

// RecreateResolved validates the complete desired private spec and persistent
// attachment contract before the first stop/destroy mutation.
func (m Manager) RecreateResolved(ctx context.Context, requested spec.Resolved) (Status, error) {
	m.ConfiguredDataRoot = requested.DataRoot
	var err error
	requested, err = m.materializeDataRoot(requested)
	if err != nil {
		return Status{}, err
	}
	if err := validateResolved(requested); err != nil {
		return Status{}, err
	}
	projectValue, err := m.openProject(false)
	if err != nil {
		return Status{}, err
	}
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil || projectState.Resolved.Network != "private" {
		return Status{}, errors.New("current project has no valid private state")
	}
	if err := validatePrivateRecreatePersistent(projectValue, projectState.Resolved, requested); err != nil {
		return Status{}, err
	}
	profile, err := m.nativeProfile()
	if err != nil {
		return Status{}, err
	}
	if _, err := m.preflight(ctx, profile, requested); err != nil {
		return Status{}, err
	}
	destroyManager := m
	selected, err := selectedNodeNames(projectState.Resolved, m.Nodes)
	if err != nil {
		return Status{}, err
	}
	if len(selected) != len(projectState.Resolved.Nodes) {
		destroyManager.allowPartialDestroy = true
	}
	if _, err := destroyManager.Destroy(ctx); err != nil {
		return Status{}, err
	}
	return m.Up(ctx, requested)
}

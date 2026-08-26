package quick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/farrow/internal/disk"
	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/persistent"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func normalizedFilesystem(value string) string {
	if value == "" {
		return "auto"
	}
	return value
}

func quickPersistentIdentities(projectValue project.Project, resolved spec.Resolved) ([]persistent.Identity, error) {
	if len(resolved.Nodes) != 1 || resolved.Nodes[0].Name != nodeName {
		return nil, errors.New("quick persistent disk identity requires the exact meta node")
	}
	result := make([]persistent.Identity, 0, len(resolved.Nodes[0].Disks))
	for _, definition := range resolved.Nodes[0].Disks {
		if !definition.Persistent {
			continue
		}
		serial, err := identity.DiskSerial(projectValue.Marker.ProjectID, nodeName, "data")
		if err != nil {
			return nil, err
		}
		result = append(result, persistent.Identity{
			ProjectID: projectValue.Marker.ProjectID, Node: nodeName, Name: definition.Name,
			Serial: serial, Size: definition.Size, Mount: definition.Mount, Filesystem: normalizedFilesystem(definition.Filesystem),
		})
	}
	return result, nil
}

func quickPersistentStateIdentities(projectValue project.Project, node state.NodeState) []persistent.Identity {
	result := make([]persistent.Identity, 0, len(node.DataDisks))
	for _, dataDisk := range node.DataDisks {
		if dataDisk.Persistent {
			result = append(result, persistent.Identity{ProjectID: projectValue.Marker.ProjectID, Node: node.Node, Name: dataDisk.Name, Serial: dataDisk.Serial, Size: dataDisk.Size, Mount: dataDisk.Mount})
		}
	}
	return result
}

func validateQuickPersistentState(projectValue project.Project, resolved spec.Resolved, node state.NodeState) ([]persistent.Identity, error) {
	desired, err := quickPersistentIdentities(projectValue, resolved)
	if err != nil {
		return nil, err
	}
	if _, err := persistent.ValidateDesired(projectValue, desired); err != nil {
		return nil, err
	}
	stateIdentities := quickPersistentStateIdentities(projectValue, node)
	if len(stateIdentities) != len(desired) {
		return nil, errors.New("quick persistent disk state differs from resolved configuration")
	}
	for index := range desired {
		if stateIdentities[index].Name != desired[index].Name || stateIdentities[index].Serial != desired[index].Serial || stateIdentities[index].Size != desired[index].Size || stateIdentities[index].Mount != desired[index].Mount {
			return nil, errors.New("quick persistent disk size, mount, or serial differs from resolved configuration")
		}
	}
	return desired, nil
}

func validateQuickDestroyDirectory(projectValue project.Project, node state.NodeState) error {
	nodeDir, err := projectValue.NodeDir(node.Node)
	if err != nil {
		return err
	}
	expected := map[string]struct{}{
		node.RootDisk: {}, node.Seed: {}, filepath.Join(nodeDir, "state.json"): {},
		filepath.Join(nodeDir, "serial.log"): {}, filepath.Join(nodeDir, "qemu.log"): {}, filepath.Join(nodeDir, "events.jsonl"): {},
	}
	if node.NVRAM != "" {
		expected[node.NVRAM] = struct{}{}
	}
	for _, dataDisk := range node.DataDisks {
		inside, err := fsutil.IsWithin(nodeDir, dataDisk.Path)
		if err != nil {
			return err
		}
		if inside {
			expected[dataDisk.Path] = struct{}{}
		} else if !dataDisk.Persistent {
			return fmt.Errorf("non-persistent quick disk escapes node root: %s", dataDisk.Path)
		}
	}
	entries, err := os.ReadDir(nodeDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(nodeDir, entry.Name())
		if _, ok := expected[path]; !ok {
			return fmt.Errorf("refuse quick destroy with unexpected node artifact %s", path)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse quick destroy with unsafe node artifact %s", path)
		}
	}
	return nil
}

func resolveQuickDataDisk(ctx context.Context, projectValue project.Project, resolved spec.Resolved, manager disk.Manager, nodeDir string, definition spec.Disk) (string, string, error) {
	serial, err := identity.DiskSerial(projectValue.Marker.ProjectID, nodeName, "data")
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(nodeDir, "data.qcow2")
	if !definition.Persistent {
		if _, err := manager.CreateBlank(ctx, path, definition.Size); err != nil {
			return "", "", err
		}
		return path, serial, nil
	}
	desired, err := quickPersistentIdentities(projectValue, resolved)
	if err != nil {
		return "", "", err
	}
	identityValue := desired[0]
	record, found, err := persistent.Find(projectValue, desired, identityValue)
	if err != nil {
		return "", "", err
	}
	if !found {
		if _, err := manager.CreateBlank(ctx, path, definition.Size); err != nil {
			return "", "", err
		}
		return path, serial, nil
	}
	info, err := manager.Inspect(ctx, record.Path)
	if err != nil {
		return "", "", fmt.Errorf("inspect retained quick disk: %w", err)
	}
	if err := disk.ValidateRuntime(info, false); err != nil || info.VirtualSize != definition.Size {
		return "", "", fmt.Errorf("retained quick disk virtual size is incompatible with desired size")
	}
	return record.Path, serial, nil
}

// PersistentDisks returns a strict inventory suitable for a CLI confirmation
// prompt. It does not mutate the project.
func (m Manager) PersistentDisks() ([]persistent.Record, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return nil, err
	}
	return persistent.Inventory(projectValue)
}

// DeletePersistent is the only quick API which deletes retained data disks.
// The node must already have been safely destroyed; interactive double
// confirmation belongs at the CLI boundary.
func (m Manager) DeletePersistent(ctx context.Context) ([]persistent.Record, error) {
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
	store := state.Store{Project: projectValue}
	projectState, err := store.ReadProject()
	if err != nil {
		return nil, err
	}
	if _, err := readAbsence(projectValue, projectState); err != nil {
		return nil, errors.New("refuse persistent disk deletion until the quick node is safely destroyed")
	}
	nodeDir, err := projectValue.NodeDir(nodeName)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(nodeDir); err == nil {
		return nil, errors.New("refuse persistent disk deletion while the quick node directory exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return persistent.DeleteAll(projectValue)
}

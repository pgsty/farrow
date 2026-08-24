package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/piglet/internal/disk"
	"github.com/pgsty/piglet/internal/identity"
	"github.com/pgsty/piglet/internal/lock"
	"github.com/pgsty/piglet/internal/persistent"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

func privateFilesystem(value string) string {
	if value == "" {
		return "auto"
	}
	return value
}

func privatePersistentIdentities(projectValue project.Project, resolved spec.Resolved) ([]persistent.Identity, error) {
	result := make([]persistent.Identity, 0)
	for _, node := range resolved.Nodes {
		for _, definition := range node.Disks {
			if !definition.Persistent {
				continue
			}
			serial, err := identity.DiskSerial(projectValue.Marker.ProjectID, node.Name, definition.Name)
			if err != nil {
				return nil, err
			}
			result = append(result, persistent.Identity{
				ProjectID: projectValue.Marker.ProjectID, Node: node.Name, Name: definition.Name,
				Serial: serial, Size: definition.Size, Mount: definition.Mount, Filesystem: privateFilesystem(definition.Filesystem),
			})
		}
	}
	return result, nil
}

func validatePrivatePersistentDesired(projectValue project.Project, resolved spec.Resolved) error {
	desired, err := privatePersistentIdentities(projectValue, resolved)
	if err != nil {
		return err
	}
	_, err = persistent.ValidateDesired(projectValue, desired)
	return err
}

func privatePrepareProject(config PrepareConfig) project.Project {
	return project.Project{Root: config.ProjectRoot, Marker: project.Marker{ProjectID: config.Plan.ProjectID}}
}

func validatePrivatePersistentState(projectValue project.Project, projectState state.ProjectState, nodes []state.NodeState) ([]persistent.Identity, error) {
	desired, err := privatePersistentIdentities(projectValue, projectState.Resolved)
	if err != nil {
		return nil, err
	}
	if _, err := persistent.ValidateDesired(projectValue, desired); err != nil {
		return nil, err
	}
	stateDisks := make(map[string]state.DataDisk)
	for _, node := range nodes {
		for _, dataDisk := range node.DataDisks {
			if dataDisk.Persistent {
				if err := persistent.ValidateSource(projectValue, dataDisk.Path); err != nil {
					return nil, err
				}
				stateDisks[node.Node+"\x00"+dataDisk.Name] = dataDisk
			}
		}
	}
	if len(stateDisks) != len(desired) {
		return nil, errors.New("private persistent disk state differs from resolved configuration")
	}
	for _, identityValue := range desired {
		dataDisk, ok := stateDisks[identityValue.Node+"\x00"+identityValue.Name]
		if !ok || dataDisk.Serial != identityValue.Serial || dataDisk.Size != identityValue.Size || dataDisk.Mount != identityValue.Mount {
			return nil, fmt.Errorf("private persistent disk %s/%s has incompatible size, mount, or serial", identityValue.Node, identityValue.Name)
		}
	}
	return desired, nil
}

type privateDiskInspector interface {
	Inspect(context.Context, string) (disk.Info, error)
}

func resolvePrivateDataDisk(ctx context.Context, config PrepareConfig, nodeDir string, definition spec.Disk, serial string) (string, bool, error) {
	defaultPath := filepath.Join(nodeDir, definition.Name+".qcow2")
	if !definition.Persistent {
		if _, err := config.Disks.CreateBlank(ctx, defaultPath, definition.Size); err != nil {
			return "", false, err
		}
		return defaultPath, true, nil
	}
	projectValue := privatePrepareProject(config)
	desired, err := privatePersistentIdentities(projectValue, config.Resolved)
	if err != nil {
		return "", false, err
	}
	identityValue := persistent.Identity{ProjectID: config.Plan.ProjectID, Node: filepath.Base(nodeDir), Name: definition.Name, Serial: serial, Size: definition.Size, Mount: definition.Mount, Filesystem: privateFilesystem(definition.Filesystem)}
	record, found, err := persistent.Find(projectValue, desired, identityValue)
	if err != nil {
		return "", false, err
	}
	if !found {
		if _, err := config.Disks.CreateBlank(ctx, defaultPath, definition.Size); err != nil {
			return "", false, err
		}
		return defaultPath, true, nil
	}
	if inspector, ok := config.Disks.(privateDiskInspector); ok {
		info, err := inspector.Inspect(ctx, record.Path)
		if err != nil {
			return "", false, fmt.Errorf("inspect retained private disk: %w", err)
		}
		if err := disk.ValidateRuntime(info, false); err != nil || info.VirtualSize != definition.Size {
			return "", false, fmt.Errorf("retained private disk %s/%s virtual size is incompatible", identityValue.Node, identityValue.Name)
		}
	}
	return record.Path, false, nil
}

// PersistentDisks returns the strict project inventory without mutation.
func (m Manager) PersistentDisks() ([]persistent.Record, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return nil, err
	}
	return persistent.Inventory(projectValue)
}

// DeletePersistent is the only private API which deletes retained data disks.
// Every node must already be destroyed and the global lease inactive. CLI
// confirmation is intentionally kept outside this filesystem boundary.
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
	leaseStatus, err := m.leaseStore().Inspect()
	if err != nil {
		return nil, err
	}
	if leaseStatus.Active {
		return nil, errors.New("refuse persistent disk deletion while a private lease is active")
	}
	nodesDir := filepath.Join(projectValue.Root, "nodes")
	entries, err := os.ReadDir(nodesDir)
	if err == nil && len(entries) != 0 {
		return nil, errors.New("refuse persistent disk deletion while private node artifacts exist")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return persistent.DeleteAll(projectValue)
}

package quick

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/piglet/internal/image"
	"github.com/pgsty/piglet/internal/lock"
	"github.com/pgsty/piglet/internal/platform"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/state"
)

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == fmt.Sprintf("%x", decoded)
}

func imageReferences(dataRoot string) (map[string]struct{}, error) {
	references := make(map[string]struct{})
	catalog, _, err := (image.ManifestManager{DataRoot: dataRoot}).Current()
	if err != nil {
		return nil, err
	}
	for _, entry := range catalog.Entries() {
		references[entry.SHA256] = struct{}{}
	}
	discovery, err := project.Discover(dataRoot)
	if err != nil {
		return nil, err
	}
	if len(discovery.Warnings) > 0 {
		return nil, fmt.Errorf("refuse image prune with unsafe registry entries: %v", discovery.Warnings)
	}
	for _, projectValue := range discovery.Projects {
		store := state.Store{Project: projectValue}
		nodesDir := filepath.Join(projectValue.Root, "nodes")
		entries, readErr := os.ReadDir(nodesDir)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		for _, entry := range entries {
			nodeDir, nameErr := projectValue.NodeDir(entry.Name())
			if nameErr != nil {
				return nil, nameErr
			}
			info, statErr := os.Lstat(nodeDir)
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
				return nil, fmt.Errorf("unsafe node registry entry %s/%s", projectValue.Marker.ProjectID, entry.Name())
			}
			if transaction, transactionErr := store.ReadTransaction(entry.Name()); transactionErr == nil {
				return nil, fmt.Errorf("project %s node %s has pending transaction %s", projectValue.Marker.ProjectID, entry.Name(), transaction.OperationID)
			} else if !errors.Is(transactionErr, os.ErrNotExist) {
				return nil, transactionErr
			}
			node, nodeErr := store.ReadNode(entry.Name())
			if nodeErr != nil {
				return nil, fmt.Errorf("read project %s node %s before image prune: %w", projectValue.Marker.ProjectID, entry.Name(), nodeErr)
			}
			if !validDigest(node.Image.Digest) {
				return nil, fmt.Errorf("project %s node %s has invalid image digest", projectValue.Marker.ProjectID, entry.Name())
			}
			references[node.Image.Digest] = struct{}{}
		}
	}
	return references, nil
}

func (m Manager) PruneImages(ctx context.Context, apply bool) (image.PruneReport, error) {
	dataRoot, err := m.dataRoot()
	if err != nil {
		return image.PruneReport{}, err
	}
	profile, err := platform.Native()
	if err != nil {
		return image.PruneReport{}, err
	}
	store, err := m.imageStore(profile, dataRoot)
	if err != nil {
		return image.PruneReport{}, err
	}
	resolve := func() (map[string]struct{}, error) {
		lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		allocator, err := lock.Acquire(lockContext, filepath.Join(dataRoot, "locks", "allocator.lock"), false)
		if err != nil {
			return nil, err
		}
		defer allocator.Release()
		return imageReferences(dataRoot)
	}
	return store.Prune(ctx, apply, resolve)
}

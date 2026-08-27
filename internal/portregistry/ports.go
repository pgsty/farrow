// Package portregistry reads the ownership-bounded project registry when
// allocating host ports shared by Quick and Private runtimes.
package portregistry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/state"
)

// Reserved returns every non-absent SSH and forwarded host port. Unsafe or
// unreadable registry entries fail closed rather than risking a duplicate bind.
func Reserved(dataRoot string) (map[uint16]struct{}, error) {
	reserved := make(map[uint16]struct{})
	discovery, err := project.Discover(dataRoot)
	if err != nil {
		return nil, err
	}
	if len(discovery.Warnings) > 0 {
		return nil, fmt.Errorf("refuse port allocation with unsafe project registry entries: %v", discovery.Warnings)
	}
	for _, projectValue := range discovery.Projects {
		nodesDir := filepath.Join(projectValue.Root, "nodes")
		entries, err := os.ReadDir(nodesDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		store := state.Store{Project: projectValue}
		for _, entry := range entries {
			if !entry.IsDir() {
				return nil, fmt.Errorf("unexpected non-directory node registry entry %s", entry.Name())
			}
			node, err := store.ReadNode(entry.Name())
			if err != nil {
				return nil, err
			}
			if node.Phase == state.Absent {
				continue
			}
			if node.SSHPort != 0 {
				reserved[node.SSHPort] = struct{}{}
			}
			for _, forward := range node.Forwards {
				reserved[forward.Host] = struct{}{}
			}
		}
	}
	return reserved, nil
}

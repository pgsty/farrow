package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/state"
)

// imageService resolves the data root exactly like the lifecycle does — the
// current project's recorded root when one exists, the configured default
// otherwise — and returns the image façade over it.
func imageService(repository string, progress activity.Reporter) (image.Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return image.Service{}, err
	}
	dataRoot := ""
	if current, openErr := project.Open(cwd); openErr == nil {
		dataRoot = current.DataRoot
	} else if missingPathError(openErr) {
		dataRoot, err = project.ResolveDataRoot(cwd, nil)
		if err != nil {
			return image.Service{}, err
		}
	} else {
		return image.Service{}, openErr
	}
	return image.Service{DataRoot: dataRoot, Repository: repository, Progress: progress}, nil
}

func validImageDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == fmt.Sprintf("%x", decoded)
}

// nodeImageReferences collects every image digest a registered node still
// references, so prune never deletes a base image in use.
func nodeImageReferences(dataRoot string) (map[string]struct{}, error) {
	references := make(map[string]struct{})
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
			if transaction, transactionErr := store.ReadTransaction(entry.Name()); transactionErr == nil {
				return nil, fmt.Errorf("node %s has pending transaction %s", entry.Name(), transaction.OperationID)
			} else if !errors.Is(transactionErr, os.ErrNotExist) {
				return nil, transactionErr
			}
			node, nodeErr := store.ReadNode(entry.Name())
			if nodeErr != nil {
				return nil, fmt.Errorf("read node %s before image prune: %w", entry.Name(), nodeErr)
			}
			if !validImageDigest(node.Image.Digest) {
				return nil, fmt.Errorf("node %s has an invalid image digest", entry.Name())
			}
			references[node.Image.Digest] = struct{}{}
		}
	}
	return references, nil
}

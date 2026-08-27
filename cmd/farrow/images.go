package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/state"
)

// imageService resolves the deployment data root and returns the image
// façade over it.
func imageService(repository string, progress activity.Reporter) (image.Service, error) {
	dataRoot, err := state.ResolveDataRoot()
	if err != nil {
		return image.Service{}, err
	}
	return image.Service{DataRoot: dataRoot, Repository: repository, Progress: progress}, nil
}

func validImageDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == fmt.Sprintf("%x", decoded)
}

// nodeImageReferences collects every image digest a deployment node still
// references, so prune never deletes a base image in use.
func nodeImageReferences(dataRoot string) (map[string]struct{}, error) {
	references := make(map[string]struct{})
	store := state.Store{Root: dataRoot}
	entries, readErr := os.ReadDir(filepath.Join(dataRoot, "nodes"))
	if errors.Is(readErr, os.ErrNotExist) {
		return references, nil
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
	return references, nil
}

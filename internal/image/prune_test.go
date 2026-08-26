package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/disk"
	"github.com/pgsty/farrow/internal/execx"
)

type pruneRunner struct{ virtualSize int64 }

func (runner pruneRunner) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	if len(args) == 0 || args[0] != "info" {
		return execx.Result{}, fmt.Errorf("unexpected qemu-img args: %v", args)
	}
	data, _ := json.Marshal(disk.Info{Filename: args[len(args)-1], Format: "qcow2", VirtualSize: runner.virtualSize})
	return execx.Result{Stdout: data}, nil
}

func writeCachedFixture(t *testing.T, store Store, content string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(hash[:])
	imagePath, _ := store.Path(digest)
	if err := os.WriteFile(imagePath, []byte(content), 0o444); err != nil {
		t.Fatal(err)
	}
	metadata := Metadata{
		Schema: MetadataSchema, Digest: digest, Format: "qcow2", VirtualSize: 1 << 30,
		ArtifactSize: int64(len(content)), Source: "test:fixture", ImportedAt: time.Unix(1, 0).UTC(),
	}
	data, _ := json.Marshal(metadata)
	metadataPath, _ := store.metadataPath(digest)
	if err := os.WriteFile(metadataPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestPruneDryRunAndApplyExactUnreferencedPairs(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	for _, directory := range []string{filepath.Join(dataRoot, "cache", "images", "sha256"), filepath.Join(dataRoot, "locks")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store := Store{DataRoot: dataRoot, QEMUImg: "/fake/qemu-img", Runner: pruneRunner{virtualSize: 1 << 30}}
	keep := writeCachedFixture(t, store, "keep-image")
	remove := writeCachedFixture(t, store, "remove-image")
	resolver := func() (map[string]struct{}, error) { return map[string]struct{}{keep: {}}, nil }

	dryRun, err := store.Prune(context.Background(), false, resolver)
	if err != nil || len(dryRun.Items) != 1 || dryRun.Items[0].Digest != remove || dryRun.Items[0].Applied {
		t.Fatalf("dry-run prune = %#v, %v", dryRun, err)
	}
	for _, digest := range []string{keep, remove} {
		path, _ := store.Path(digest)
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("dry run removed %s: %v", digest, err)
		}
	}

	applied, err := store.Prune(context.Background(), true, resolver)
	if err != nil || len(applied.Items) != 1 || !applied.Items[0].Applied {
		t.Fatalf("applied prune = %#v, %v", applied, err)
	}
	removedImage, _ := store.Path(remove)
	removedMetadata, _ := store.metadataPath(remove)
	for _, pathname := range []string{removedImage, removedMetadata} {
		if _, err := os.Lstat(pathname); !os.IsNotExist(err) {
			t.Fatalf("pruned path remains: %s: %v", pathname, err)
		}
	}
	keptImage, _ := store.Path(keep)
	if _, err := os.Lstat(keptImage); err != nil {
		t.Fatalf("referenced image removed: %v", err)
	}
}

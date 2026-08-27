package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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

func writeCachedFixture(t *testing.T, store Store, family, filename, content string) (Entry, string) {
	t.Helper()
	hash := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(hash[:])
	entry := Entry{Alias: family, File: family + "/" + filename, SHA256: digest, Format: "qcow2", ArtifactSize: int64(len(content)), VirtualSize: 1 << 30}
	imagePath, err := store.Path(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte(content), 0o444); err != nil {
		t.Fatal(err)
	}
	return entry, imagePath
}

func TestPruneDryRunAndApplyReadableImages(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := Store{DataRoot: dataRoot, QEMUImg: "/fake/qemu-img", Runner: pruneRunner{virtualSize: 1 << 30}}
	keep, keepPath := writeCachedFixture(t, store, "u24", "u24-1-arm64.qcow2", "keep-image")
	remove, removePath := writeCachedFixture(t, store, "u22", "u22-1-arm64.qcow2", "remove-image")
	resolver := func() (map[string]struct{}, error) { return map[string]struct{}{keep.SHA256: {}}, nil }

	dryRun, err := store.Prune(context.Background(), false, resolver)
	if err != nil || len(dryRun.Items) != 1 || dryRun.Items[0].Digest != remove.SHA256 || dryRun.Items[0].Applied {
		t.Fatalf("dry-run prune = %#v, %v", dryRun, err)
	}
	for _, pathname := range []string{keepPath, removePath} {
		if _, err := os.Lstat(pathname); err != nil {
			t.Fatalf("dry run removed %s: %v", pathname, err)
		}
	}

	applied, err := store.Prune(context.Background(), true, resolver)
	if err != nil || len(applied.Items) != 1 || !applied.Items[0].Applied {
		t.Fatalf("applied prune = %#v, %v", applied, err)
	}
	if _, err := os.Lstat(removePath); !os.IsNotExist(err) {
		t.Fatalf("pruned path remains: %s: %v", removePath, err)
	}
	if _, err := os.Lstat(keepPath); err != nil {
		t.Fatalf("referenced image removed: %v", err)
	}
}

func TestPruneDryRunAndApplyOrphanedStagingFiles(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	family := filepath.Join(dataRoot, "images", "u24")
	for _, directory := range []string{family, filepath.Join(dataRoot, "locks")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	staging := filepath.Join(family, ".download-orphan.partial")
	unmanaged := filepath.Join(family, ".notes.partial")
	for _, pathname := range []string{staging, unmanaged} {
		if err := os.WriteFile(pathname, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := Store{DataRoot: dataRoot, QEMUImg: "/fake/qemu-img", Runner: pruneRunner{virtualSize: 1 << 30}}
	resolver := func() (map[string]struct{}, error) { return map[string]struct{}{}, nil }
	dryRun, err := store.Prune(context.Background(), false, resolver)
	if err != nil || len(dryRun.Items) != 1 || dryRun.Items[0].Kind != "staging" || dryRun.Items[0].ImagePath != staging || dryRun.Items[0].Digest != "" || dryRun.Items[0].Applied {
		t.Fatalf("staging dry-run prune = %#v, %v", dryRun, err)
	}
	if _, err := os.Lstat(staging); err != nil {
		t.Fatalf("dry run removed staging file: %v", err)
	}
	applied, err := store.Prune(context.Background(), true, resolver)
	if err != nil || len(applied.Items) != 1 || applied.Items[0].Kind != "staging" || !applied.Items[0].Applied {
		t.Fatalf("staging apply prune = %#v, %v", applied, err)
	}
	if _, err := os.Lstat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging file remains: %v", err)
	}
	if _, err := os.Lstat(unmanaged); err != nil {
		t.Fatalf("unmanaged partial file was removed: %v", err)
	}
}

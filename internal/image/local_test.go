package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationNamedLocalImportIsImmutableAndResolvable(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	path, metadata, err := store.Import(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	entry, resolvedPath, resolvedMetadata, err := store.RegisterLocalAlias(context.Background(), "local-u24", path, metadata, "arm64", "uefi", "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Alias != "local-u24" || entry.SHA256 != metadata.Digest || entry.ArtifactSize != metadata.ArtifactSize || resolvedPath != path || resolvedMetadata.Digest != metadata.Digest {
		t.Fatalf("registered local alias = %#v %s %#v", entry, resolvedPath, resolvedMetadata)
	}
	again, againPath, _, err := store.ResolveLocalAlias(context.Background(), "local-u24", "arm64")
	if err != nil || again.SHA256 != metadata.Digest || againPath != path {
		t.Fatalf("resolved local alias = %#v %s %v", again, againPath, err)
	}
	info, err := os.Lstat(store.localAliasesPath())
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("local alias registry mode = %v, %v", info, err)
	}
	if _, _, _, err := store.RegisterLocalAlias(context.Background(), "local-u24", path, metadata, "arm64", "bios", "ubuntu"); err == nil {
		t.Fatal("local alias metadata was mutated")
	}
	if _, _, _, err := store.RegisterLocalAlias(context.Background(), "u24", path, metadata, "arm64", "uefi", "ubuntu"); err == nil {
		t.Fatal("catalog-shaped local alias was accepted")
	}
	names, err := LocalAliasNames(store.DataRoot)
	if err != nil || len(names) != 1 || names[0] != "local-u24" {
		t.Fatalf("local alias names = %v, %v", names, err)
	}
}

func TestLocalAliasNamesIsSideEffectFreeWhenRegistryIsAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	names, err := LocalAliasNames(root)
	if err != nil || len(names) != 0 {
		t.Fatalf("local alias names = %v, %v", names, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "images")); !os.IsNotExist(err) {
		t.Fatalf("completion lookup created image state: %v", err)
	}
}

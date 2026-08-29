package image

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListEmbeddedCatalogWithoutQEMUImg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	entries, state, err := (Service{DataRoot: t.TempDir()}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != "embedded" || len(entries) != 17 {
		t.Fatalf("manifest=%#v entries=%d", state, len(entries))
	}
}

func TestLookupArchUsesEmbeddedCatalogWithoutDownloading(t *testing.T) {
	t.Parallel()
	service := Service{DataRoot: t.TempDir()}
	entry, err := service.LookupArch(context.Background(), "centos79", "amd64")
	if err != nil || entry.Alias != "el7" || entry.Arch != "amd64" || entry.Boot != "bios" {
		t.Fatalf("EL7 lookup = %#v, %v", entry, err)
	}
	if _, err := service.LookupArch(context.Background(), "el7", "arm64"); err == nil {
		t.Fatal("EL7 arm64 lookup unexpectedly succeeded")
	}
}

func TestExplicitUnsignedLocalRepositoryIsSelfContained(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	catalog := EmbeddedCatalog()
	catalog.Version = 1
	record := catalog.Images["d13"]
	record.Aliases = append(record.Aliases, "lab-default")
	catalog.Images["d13"] = record
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, CatalogFilename), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, state, err := (Service{DataRoot: t.TempDir(), Repository: repository}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 17 || state.ActiveVersion != 1 || state.KeyID != "" || state.Source != "local:"+filepath.Join(repository, CatalogFilename) {
		t.Fatalf("local repository list = %d entries, state %#v", len(entries), state)
	}
}

func TestListIncludesStaleForeignLocalAliasWithoutQEMUImg(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	imagesRoot := filepath.Join(dataRoot, "images")
	if err := os.Mkdir(imagesRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	registry := LocalAliases{Schema: LocalAliasesSchema, Aliases: map[string]LocalAlias{
		"local-foreign": {
			Name: "local-foreign", File: "local/missing.qcow2", Digest: strings.Repeat("a", 64),
			ArtifactSize: 1, VirtualSize: 1, Arch: "amd64", Boot: "uefi", SourceUser: "dba", CreatedAt: time.Now().UTC(),
		},
	}}
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imagesRoot, "local-images.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _, err := (Service{DataRoot: dataRoot}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		found = found || entry.Alias == "local-foreign"
	}
	if !found {
		t.Fatal("stale foreign local alias missing from metadata-only list")
	}
}

func TestSyncAndResetHonorFARROWREPOStateKey(t *testing.T) {
	repository := t.TempDir()
	dataRoot := t.TempDir()
	t.Setenv("FARROW_REPO", repository)
	catalog := EmbeddedCatalog()
	catalog.Version = 1
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repository, CatalogFilename)
	if err := os.WriteFile(source, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{DataRoot: dataRoot}
	if state, err := service.SyncManifest(context.Background(), source, false); err != nil || state.ActiveVersion != 1 {
		t.Fatalf("environment repository sync = %#v, %v", state, err)
	}
	manager := ManifestManager{DataRoot: dataRoot, Repository: repository, AllowUnsigned: true}
	if state, err := manager.readState(); err != nil || state.ActiveVersion != 1 {
		t.Fatalf("environment repository state = %#v, %v", state, err)
	}
	if _, err := service.ResetManifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.readState(); err != nil || state.Active != "embedded" || state.HighestVersion != 1 {
		t.Fatalf("environment repository reset lost high-water state: %#v, %v", state, err)
	}
}

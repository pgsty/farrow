package image

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedCatalogRoundTrip(t *testing.T) {
	t.Parallel()
	data, err := EmbeddedCatalogBytes()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := strictCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Entry("ubuntu2404", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if entry.SHA256 != "aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476" || entry.ArtifactSize != 618417664 || entry.VirtualSize != 3758096384 || catalog.Version != EmbeddedManifestVersion || catalog.Defaults.Image != "d13" || entry.Channel != "stable" {
		t.Fatalf("entry/catalog = %#v %#v", entry, catalog)
	}
	if len(catalog.Images) != 9 || len(catalog.Entries()) != 27 {
		t.Fatalf("embedded catalog matrix = %d images / %d entries", len(catalog.Images), len(catalog.Entries()))
	}
	for _, alias := range formalAliases {
		record, ok := catalog.Images[alias]
		wantVersions := 1
		switch alias {
		case "el9":
			wantVersions = 4
		case "el10":
			wantVersions = 3
		}
		if !ok || len(record.Versions) != wantVersions || record.Channels["stable"] == "" {
			t.Errorf("catalog image %s = %#v", alias, record)
			continue
		}
		for _, version := range record.Versions {
			want := 2
			if alias == "el7" {
				want = 1
			}
			if len(version.Variants) != want {
				t.Errorf("catalog image %s architectures = %v", alias, version.Variants)
			}
		}
	}
}

func TestEmbeddedCatalogSupportPolicy(t *testing.T) {
	t.Parallel()
	catalog := EmbeddedCatalog()
	for _, test := range []struct {
		reference string
		arch      string
		status    string
	}{
		{"d12", "amd64", "supported"},
		{"d13", "arm64", "supported"},
		{"el8", "amd64", "supported"},
		{"el9@9.8", "arm64", "supported"},
		{"el9@9.7", "amd64", "supported"},
		{"el9@9.6", "arm64", "deprecated"},
		{"el9@9.3", "amd64", "deprecated"},
		{"el10@10.2", "arm64", "supported"},
		{"el10@10.1", "amd64", "supported"},
		{"el10@10.0", "arm64", "deprecated"},
		{"el7", "amd64", "deprecated"},
		{"u22", "arm64", "supported"},
		{"u24", "amd64", "supported"},
		{"u26", "arm64", "supported"},
	} {
		entry, err := catalog.Entry(test.reference, test.arch)
		if err != nil || entry.Status != test.status {
			t.Errorf("catalog status %s/%s = %q, %v; want %q", test.reference, test.arch, entry.Status, err, test.status)
		}
	}
}

func TestLegacySchemaTwoCatalogConvertsWithoutChangingRepositoryPath(t *testing.T) {
	t.Parallel()
	legacy := legacyCatalog{
		Schema: 2, Version: 2026082801, GeneratedAt: json.RawMessage(`"2026-08-28T00:00:00Z"`),
		Images: map[string]legacyCatalogImage{
			"u24": {
				Default: "1",
				Releases: map[string]map[string]legacyArtifact{
					"1": {
						"arm64": legacyArtifact{
							File: "u24/u24-1-arm64.qcow2", Upstream: "https://example.test/u24-1-arm64.qcow2",
							SHA256: strings.Repeat("a", 64), Format: "qcow2", ArtifactSize: 1, VirtualSize: 1,
							Boot: "uefi", Status: "testing", Provenance: "legacy fixture",
						},
					},
				},
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := strictCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Entry("u24", "arm64")
	if err != nil || entry.File != "u24/u24-1-arm64.qcow2" || entry.CacheFile != "u24/u24-1-arm64.qcow2" {
		t.Fatalf("converted legacy entry = %#v, %v", entry, err)
	}
}

func TestPublishedSchemaTwoCatalogGoldenConverts(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "catalog-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := strictCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != ManifestSchema || catalog.Version != 2026082801 || catalog.Defaults.Image != "u24" || len(catalog.Entries()) != 17 {
		t.Fatalf("converted published catalog = %#v entries=%d", catalog, len(catalog.Entries()))
	}
	for _, test := range []struct {
		reference string
		arch      string
	}{
		{"u24", "arm64"},
		{"d13", "amd64"},
	} {
		if _, err := catalog.Entry(test.reference, test.arch); err != nil {
			t.Errorf("published v2 entry %s/%s: %v", test.reference, test.arch, err)
		}
	}
}

func TestCatalogRejectsUnknownAndTrailing(t *testing.T) {
	t.Parallel()
	data, _ := EmbeddedCatalogBytes()
	data = bytes.Replace(data, []byte(`"schema": 3`), []byte(`"schema": 3, "unknown": true`), 1)
	if _, err := strictCatalog(data); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	valid, _ := EmbeddedCatalogBytes()
	if _, err := strictCatalog(append(valid, []byte(`{}`)...)); err == nil {
		t.Fatal("trailing manifest JSON accepted")
	}
}

func TestCatalogRejectsIncompleteArtifactAndMovingURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Artifact)
	}{
		{name: "artifact size", mutate: func(artifact *Artifact) { artifact.ArtifactSize = 0 }},
		{name: "virtual size", mutate: func(artifact *Artifact) { artifact.VirtualSize = 0 }},
		{name: "unsafe file", mutate: func(artifact *Artifact) { artifact.File = "../image.qcow2" }},
		{name: "credential URL", mutate: func(artifact *Artifact) { artifact.Upstream = "https://user:secret@example.test/image.qcow2" }},
		{name: "query URL", mutate: func(artifact *Artifact) { artifact.Upstream = "https://example.test/image.qcow2?token=secret" }},
		{name: "fragment URL", mutate: func(artifact *Artifact) { artifact.Upstream = "https://example.test/image.qcow2#release" }},
		{name: "latest URL", mutate: func(artifact *Artifact) {
			artifact.Upstream = "https://cloud.debian.org/images/cloud/trixie/latest/image.qcow2"
		}},
		{name: "current URL", mutate: func(artifact *Artifact) {
			artifact.Upstream = "https://cloud-images.ubuntu.com/noble/current/image.img"
		}},
		{name: "release symlink URL", mutate: func(artifact *Artifact) {
			artifact.Upstream = "https://cloud-images.ubuntu.com/minimal/releases/noble/release/image.img"
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := EmbeddedCatalog()
			record := catalog.Images["u24"]
			version := record.Versions["20260801.0.0"]
			artifact := version.Variants["arm64"]
			test.mutate(&artifact)
			version.Variants["arm64"] = artifact
			record.Versions["20260801.0.0"] = version
			catalog.Images["u24"] = record
			if err := catalog.Validate(); err == nil {
				t.Fatal("invalid catalog artifact unexpectedly accepted")
			}
		})
	}
}

func TestCatalogRejectsReservedLocalAliasNamespace(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*Catalog){
		func(catalog *Catalog) {
			catalog.Images["local"] = catalog.Images["u24"]
			delete(catalog.Images, "u24")
		},
		func(catalog *Catalog) {
			catalog.Images["local-custom"] = catalog.Images["u24"]
			delete(catalog.Images, "u24")
		},
		func(catalog *Catalog) {
			record := catalog.Images["u24"]
			record.Aliases = append(record.Aliases, "local")
			catalog.Images["u24"] = record
		},
		func(catalog *Catalog) {
			record := catalog.Images["u24"]
			record.Aliases = append(record.Aliases, "local-custom")
			catalog.Images["u24"] = record
		},
	} {
		catalog := EmbeddedCatalog()
		mutate(&catalog)
		if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("catalog reserved namespace error = %v", err)
		}
	}
}

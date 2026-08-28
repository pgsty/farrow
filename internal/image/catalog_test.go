package image

import (
	"bytes"
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
	if entry.SHA256 != "aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476" || entry.ArtifactSize != 618417664 || entry.VirtualSize != 3758096384 || catalog.Version != EmbeddedManifestVersion || !catalog.GeneratedAt.Equal(embeddedManifestGeneratedAt) {
		t.Fatalf("entry/catalog = %#v %#v", entry, catalog)
	}
	if len(catalog.Images) != 9 || len(catalog.Entries()) != 17 {
		t.Fatalf("embedded catalog matrix = %d images / %d entries", len(catalog.Images), len(catalog.Entries()))
	}
	for _, alias := range formalAliases {
		record, ok := catalog.Images[alias]
		if !ok || len(record.Releases) != 1 || record.Default == "" {
			t.Errorf("catalog image %s = %#v", alias, record)
			continue
		}
		for _, arches := range record.Releases {
			want := 2
			if alias == "el7" {
				want = 1
			}
			if len(arches) != want {
				t.Errorf("catalog image %s architectures = %v", alias, arches)
			}
		}
	}
}

func TestCatalogRejectsUnknownAndTrailing(t *testing.T) {
	t.Parallel()
	data, _ := EmbeddedCatalogBytes()
	data = bytes.Replace(data, []byte(`"schema": 2`), []byte(`"schema": 2, "unknown": true`), 1)
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
		{name: "source user", mutate: func(artifact *Artifact) { artifact.SourceUser = " " }},
		{name: "provenance", mutate: func(artifact *Artifact) { artifact.Provenance = "" }},
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
			artifact := record.Releases["20260801.0.0"]["arm64"]
			test.mutate(&artifact)
			record.Releases["20260801.0.0"]["arm64"] = artifact
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
		if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), "reserved local image namespace") {
			t.Fatalf("catalog reserved namespace error = %v", err)
		}
	}
}

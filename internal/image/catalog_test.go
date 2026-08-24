package image

import (
	"bytes"
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
	if len(catalog.Images) != 7 || len(catalog.Entries()) != 14 {
		t.Fatalf("embedded catalog matrix = %d images / %d entries", len(catalog.Images), len(catalog.Entries()))
	}
	for _, alias := range formalAliases {
		record, ok := catalog.Images[alias]
		if !ok || len(record.Releases) != 1 {
			t.Errorf("catalog image %s = %#v", alias, record)
			continue
		}
		for _, arches := range record.Releases {
			if len(arches) != 2 {
				t.Errorf("catalog image %s architectures = %v", alias, arches)
			}
		}
	}
}

func TestCatalogRejectsUnknownAndTrailing(t *testing.T) {
	t.Parallel()
	data, _ := EmbeddedCatalogBytes()
	data = bytes.Replace(data, []byte(`"schema": 1`), []byte(`"schema": 1, "unknown": true`), 1)
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
		{name: "latest URL", mutate: func(artifact *Artifact) {
			artifact.URL = "https://cloud.debian.org/images/cloud/trixie/latest/image.qcow2"
		}},
		{name: "current URL", mutate: func(artifact *Artifact) { artifact.URL = "https://cloud-images.ubuntu.com/noble/current/image.img" }},
		{name: "release symlink URL", mutate: func(artifact *Artifact) {
			artifact.URL = "https://cloud-images.ubuntu.com/minimal/releases/noble/release/image.img"
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

package image

import (
	"path/filepath"
	"testing"
)

func TestRepositorySources(t *testing.T) {
	t.Parallel()
	catalog, err := RepositoryCatalogSource("http://m0/repos/farrow/")
	if err != nil || catalog != "http://m0/repos/farrow/catalog.json" {
		t.Fatalf("catalog source = %q, %v", catalog, err)
	}
	artifact, err := RepositoryArtifactSource("http://m0/repos/farrow", "u24/u24-1-arm64.qcow2")
	if err != nil || artifact != "http://m0/repos/farrow/u24/u24-1-arm64.qcow2" {
		t.Fatalf("artifact source = %q, %v", artifact, err)
	}
	flat, err := RepositoryArtifactSource("https://repo.example/farrow", "images/d13-1-arm64.qcow2")
	if err != nil || flat != "https://repo.example/farrow/images/d13-1-arm64.qcow2" {
		t.Fatalf("flat artifact source = %q, %v", flat, err)
	}
	local := filepath.Join(t.TempDir(), "repo")
	localCatalog, err := RepositoryCatalogSource(local)
	if err != nil || localCatalog != filepath.Join(local, CatalogFilename) {
		t.Fatalf("local catalog source = %q, %v", localCatalog, err)
	}
}

func TestPublicDefaultRepository(t *testing.T) {
	t.Parallel()
	const expected = "https://repo.pigsty.cc/farrow"
	if DefaultRepositoryURL != expected {
		t.Fatalf("default repository = %q, want %q", DefaultRepositoryURL, expected)
	}
	if RepositoryAllowsUnsigned(DefaultRepositoryURL) {
		t.Fatal("the compiled default repository unexpectedly permits an unsigned catalog")
	}
}

func TestRepositoryRejectsAmbiguousLocations(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"relative/repo", "ftp://example.com/repo", "https://user@example.com/repo", "https://example.com/repo?q=1"} {
		if _, err := NormalizeRepository(value); err == nil {
			t.Errorf("unsafe repository %q accepted", value)
		}
	}
	if _, err := RepositoryArtifactSource("https://example.com/repo", "../escape.qcow2"); err == nil {
		t.Fatal("unsafe artifact path accepted")
	}
}

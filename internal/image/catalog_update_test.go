package image

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func writeCatalogFixture(t *testing.T, repository string, revision uint64, status string) {
	t.Helper()
	catalog := EmbeddedCatalog()
	catalog.Version = revision
	if status != "" {
		record := catalog.Images["d13"]
		version := record.Versions[record.Channels["stable"]]
		version.Status = status
		record.Versions[record.Channels["stable"]] = version
		catalog.Images["d13"] = record
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, CatalogFilename), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCatalogActivatesNewerRevisionAndReportsCurrent(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	writeCatalogFixture(t, repository, EmbeddedManifestVersion+1, "")
	service := Service{DataRoot: filepath.Join(t.TempDir(), "data"), Repository: repository}

	first, err := service.UpdateCatalog(context.Background())
	if err != nil || !first.Updated || first.PreviousVersion != EmbeddedManifestVersion || first.ActiveVersion != EmbeddedManifestVersion+1 || first.Repository != repository {
		t.Fatalf("first update = %#v, %v", first, err)
	}
	second, err := service.UpdateCatalog(context.Background())
	if err != nil || second.Updated || second.PreviousVersion != EmbeddedManifestVersion+1 || second.ActiveVersion != EmbeddedManifestVersion+1 {
		t.Fatalf("second update = %#v, %v", second, err)
	}
}

func TestOpenCatalogNeverTouchesTheRepository(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	service := Service{DataRoot: filepath.Join(t.TempDir(), "data"), Repository: server.URL}
	session, err := service.OpenCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.LookupArch(context.Background(), "d13", "arm64"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("local catalog operations made %d repository requests", requests.Load())
	}
	if _, err := service.UpdateCatalog(context.Background()); err == nil || requests.Load() != 1 {
		t.Fatalf("explicit update against an offline repository: err=%v requests=%d", err, requests.Load())
	}
}

func TestCatalogSessionPinsOneRevisionForTheCommand(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	writeCatalogFixture(t, repository, EmbeddedManifestVersion+1, "supported")
	service := Service{DataRoot: filepath.Join(t.TempDir(), "data"), Repository: repository}
	if _, err := service.UpdateCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := service.OpenCatalog()
	if err != nil {
		t.Fatal(err)
	}
	before, err := session.LookupArch(context.Background(), "d13", "arm64")
	if err != nil || before.Status != "supported" {
		t.Fatalf("initial session entry = %#v, %v", before, err)
	}

	writeCatalogFixture(t, repository, EmbeddedManifestVersion+2, "deprecated")
	if _, err := service.UpdateCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	pinned, err := session.LookupArch(context.Background(), "d13", "arm64")
	if err != nil || pinned.Status != "supported" || session.Manifest().ActiveVersion != EmbeddedManifestVersion+1 {
		t.Fatalf("pinned session changed revision: entry=%#v manifest=%#v err=%v", pinned, session.Manifest(), err)
	}
	current, err := service.OpenCatalog()
	if err != nil {
		t.Fatal(err)
	}
	updated, err := current.LookupArch(context.Background(), "d13", "arm64")
	if err != nil || updated.Status != "deprecated" || current.Manifest().ActiveVersion != EmbeddedManifestVersion+2 {
		t.Fatalf("new session did not observe update: entry=%#v manifest=%#v err=%v", updated, current.Manifest(), err)
	}
}

func TestUpdateCatalogRepairsMissingActiveManifest(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	writeCatalogFixture(t, repository, EmbeddedManifestVersion+4, "")
	dataRoot := filepath.Join(t.TempDir(), "data")
	service := Service{DataRoot: dataRoot, Repository: repository}
	if _, err := service.UpdateCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager := ManifestManager{DataRoot: dataRoot, Repository: repository, AllowUnsigned: true}
	_, state, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(manager.versions(), state.Active)
	if err := os.Remove(activePath); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateCatalog(context.Background())
	if err != nil {
		t.Fatalf("update did not self-heal a missing active manifest: %v", err)
	}
	if result.PreviousVersion != 0 || !result.Updated {
		t.Fatalf("repaired update result = %#v", result)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active manifest was not restored: %v", err)
	}
	if _, _, err := manager.Current(); err != nil {
		t.Fatalf("restored manifest is not current: %v", err)
	}
}

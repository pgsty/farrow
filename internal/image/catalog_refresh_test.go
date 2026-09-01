package image

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/activity"
)

func catalogRefreshFixture(t *testing.T, revision uint64) []byte {
	t.Helper()
	catalog := EmbeddedCatalog()
	catalog.Version = revision
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestCatalogRefreshTTLForceAndFailureBackoff(t *testing.T) {
	t.Parallel()
	data := catalogRefreshFixture(t, EmbeddedManifestVersion+1)
	var requests atomic.Int64
	var unavailable atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if unavailable.Load() || request.URL.Path != "/catalog.json" {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write(data)
	}))
	defer server.Close()

	manager := ManifestManager{
		DataRoot: filepath.Join(t.TempDir(), "data"), Repository: server.URL,
		AllowUnsigned: true, HTTPClient: server.Client(),
	}
	source := server.URL + "/catalog.json"
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	first, err := manager.refreshCatalogIfDue(context.Background(), source, now, DefaultCatalogTTL, CatalogRefreshBackoff)
	if err != nil || !first.Attempted || !first.Updated || first.ActiveVersion != EmbeddedManifestVersion+1 || requests.Load() != 2 {
		t.Fatalf("first refresh = %#v requests=%d err=%v", first, requests.Load(), err)
	}
	_, state, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(manager.versions(), state.Active)
	before, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	fresh, err := manager.refreshCatalogIfDue(context.Background(), source, now.Add(6*24*time.Hour), DefaultCatalogTTL, CatalogRefreshBackoff)
	if err != nil || fresh.Attempted || requests.Load() != 2 {
		t.Fatalf("fresh decision = %#v requests=%d err=%v", fresh, requests.Load(), err)
	}
	stale, err := manager.refreshCatalogIfDue(context.Background(), source, now.Add(8*24*time.Hour), DefaultCatalogTTL, CatalogRefreshBackoff)
	if err != nil || !stale.Attempted || stale.Updated || requests.Load() != 4 {
		t.Fatalf("stale refresh = %#v requests=%d err=%v", stale, requests.Load(), err)
	}
	after, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("immutable manifest mtime changed: before=%s after=%s", before.ModTime(), after.ModTime())
	}

	unavailable.Store(true)
	failureTime := now.Add(16 * 24 * time.Hour)
	failed, err := manager.refreshCatalogIfDue(context.Background(), source, failureTime, DefaultCatalogTTL, CatalogRefreshBackoff)
	if err == nil || !failed.Attempted || requests.Load() != 5 {
		t.Fatalf("failed refresh = %#v requests=%d err=%v", failed, requests.Load(), err)
	}
	backedOff, err := manager.refreshCatalogIfDue(context.Background(), source, failureTime.Add(30*time.Minute), DefaultCatalogTTL, CatalogRefreshBackoff)
	if err != nil || backedOff.Attempted || requests.Load() != 5 {
		t.Fatalf("failure backoff = %#v requests=%d err=%v", backedOff, requests.Load(), err)
	}

	unavailable.Store(false)
	forced, err := manager.refreshCatalogNow(context.Background(), source, failureTime.Add(45*time.Minute))
	if err != nil || !forced.Attempted || forced.Updated || requests.Load() != 7 {
		t.Fatalf("forced refresh = %#v requests=%d err=%v", forced, requests.Load(), err)
	}
}

func TestAutomaticCatalogRefreshFallsBackToEmbeddedAndBacksOff(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	defaultRepository := DefaultRepositoryURL
	DefaultRepositoryURL = server.URL
	t.Cleanup(func() { DefaultRepositoryURL = defaultRepository })

	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	service := Service{DataRoot: filepath.Join(t.TempDir(), "data"), Now: func() time.Time { return now }}
	first, err := service.OpenCatalog(context.Background(), CatalogRefreshIfDue)
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest().Source != "embedded" || first.Refresh().Warning == "" || requests.Load() != 1 {
		t.Fatalf("offline first open = manifest %#v refresh %#v requests=%d", first.Manifest(), first.Refresh(), requests.Load())
	}
	second, err := service.OpenCatalog(context.Background(), CatalogRefreshIfDue)
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest().Source != "embedded" || second.Refresh().Attempted || requests.Load() != 1 {
		t.Fatalf("offline backed-off open = manifest %#v refresh %#v requests=%d", second.Manifest(), second.Refresh(), requests.Load())
	}
}

func TestConcurrentCatalogRefreshUsesOneRepositoryProbe(t *testing.T) {
	t.Parallel()
	data := catalogRefreshFixture(t, EmbeddedManifestVersion+2)
	var requests atomic.Int64
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	t.Cleanup(release)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path == "/catalog.json" {
			startedOnce.Do(func() { close(requestStarted) })
			<-releaseRequest
			_, _ = writer.Write(data)
			return
		}
		http.Error(writer, "unsigned", http.StatusNotFound)
	}))
	defer server.Close()

	manager := ManifestManager{
		DataRoot: filepath.Join(t.TempDir(), "data"), Repository: server.URL,
		AllowUnsigned: true, HTTPClient: server.Client(),
	}
	source := server.URL + "/catalog.json"
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	type outcome struct {
		result CatalogRefreshResult
		err    error
	}
	results := make(chan outcome, 2)
	go func() {
		result, err := manager.refreshCatalogIfDue(context.Background(), source, now, DefaultCatalogTTL, CatalogRefreshBackoff)
		results <- outcome{result: result, err: err}
	}()
	<-requestStarted
	go func() {
		result, err := manager.refreshCatalogIfDue(context.Background(), source, now, DefaultCatalogTTL, CatalogRefreshBackoff)
		results <- outcome{result: result, err: err}
	}()
	select {
	case second := <-results:
		release()
		t.Fatalf("a concurrent refresh escaped the repository lock: %#v", second)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent refresh errors: %v, %v", first.err, second.err)
	}
	if requests.Load() != 2 || first.result.Attempted == second.result.Attempted {
		t.Fatalf("concurrent results = %#v %#v requests=%d", first.result, second.result, requests.Load())
	}
}

func TestCatalogSessionPinsOneRevisionForTheCommand(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	write := func(revision uint64, status string) {
		t.Helper()
		catalog := EmbeddedCatalog()
		catalog.Version = revision
		record := catalog.Images["d13"]
		version := record.Versions[record.Channels["stable"]]
		version.Status = status
		record.Versions[record.Channels["stable"]] = version
		catalog.Images["d13"] = record
		data, err := json.MarshalIndent(catalog, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repository, CatalogFilename), append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(EmbeddedManifestVersion+1, "supported")
	service := Service{DataRoot: filepath.Join(t.TempDir(), "data"), Repository: repository}
	session, err := service.OpenCatalog(context.Background(), CatalogRefreshNow)
	if err != nil {
		t.Fatal(err)
	}
	before, err := session.LookupArch(context.Background(), "d13", "arm64")
	if err != nil || before.Status != "supported" {
		t.Fatalf("initial session entry = %#v, %v", before, err)
	}

	write(EmbeddedManifestVersion+2, "deprecated")
	if _, err := service.UpdateCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	pinned, err := session.LookupArch(context.Background(), "d13", "arm64")
	if err != nil || pinned.Status != "supported" || session.Manifest().ActiveVersion != EmbeddedManifestVersion+1 {
		t.Fatalf("pinned session changed revision: entry=%#v manifest=%#v err=%v", pinned, session.Manifest(), err)
	}
	current, err := service.OpenCatalog(context.Background(), CatalogLocalOnly)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := current.LookupArch(context.Background(), "d13", "arm64")
	if err != nil || updated.Status != "deprecated" || current.Manifest().ActiveVersion != EmbeddedManifestVersion+2 {
		t.Fatalf("new session did not observe update: entry=%#v manifest=%#v err=%v", updated, current.Manifest(), err)
	}
}

func TestFreshCachedCatalogDoesNotReadRepositoryAndStaleFailureStillWorks(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	data := catalogRefreshFixture(t, EmbeddedManifestVersion+3)
	catalogPath := filepath.Join(repository, CatalogFilename)
	if err := os.WriteFile(catalogPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	service := Service{
		DataRoot: filepath.Join(t.TempDir(), "data"), Repository: repository,
		Now: func() time.Time { return now },
	}
	if result, err := service.UpdateCatalog(context.Background()); err != nil || !result.Updated {
		t.Fatalf("initial update = %#v, %v", result, err)
	}
	if err := os.Remove(catalogPath); err != nil {
		t.Fatal(err)
	}

	now = now.Add(6 * 24 * time.Hour)
	fresh, err := service.OpenCatalog(context.Background(), CatalogRefreshIfDue)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Refresh().Attempted || fresh.Manifest().ActiveVersion != EmbeddedManifestVersion+3 {
		t.Fatalf("fresh offline cache = manifest %#v refresh %#v", fresh.Manifest(), fresh.Refresh())
	}
	now = now.Add(2 * 24 * time.Hour)
	stale, err := service.OpenCatalog(context.Background(), CatalogRefreshIfDue)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Refresh().Attempted || stale.Refresh().Warning == "" || stale.Manifest().ActiveVersion != EmbeddedManifestVersion+3 {
		t.Fatalf("stale offline cache = manifest %#v refresh %#v", stale.Manifest(), stale.Refresh())
	}
}

func TestExplicitUpdateRepairsMissingActiveManifest(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	catalogPath := filepath.Join(repository, CatalogFilename)
	if err := os.WriteFile(catalogPath, catalogRefreshFixture(t, EmbeddedManifestVersion+4), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestResetUnsyncedRepositoryDoesNotSuppressFirstRefresh(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	service := Service{
		DataRoot: filepath.Join(t.TempDir(), "data"), Repository: repository,
		Now: func() time.Time { return now },
	}
	if _, err := service.ResetManifest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, CatalogFilename), catalogRefreshFixture(t, EmbeddedManifestVersion+5), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := service.OpenCatalog(context.Background(), CatalogRefreshIfDue)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Refresh().Attempted || session.Manifest().ActiveVersion != EmbeddedManifestVersion+5 {
		t.Fatalf("unsynced reset suppressed first refresh: manifest=%#v refresh=%#v", session.Manifest(), session.Refresh())
	}
}

func TestMalformedFreshnessSelfHeals(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, CatalogFilename), catalogRefreshFixture(t, EmbeddedManifestVersion+6), 0o600); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(t.TempDir(), "data")
	manager := ManifestManager{DataRoot: dataRoot, Repository: repository, AllowUnsigned: true}
	if err := manager.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.freshnessPath(), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{DataRoot: dataRoot, Repository: repository}
	if _, err := service.UpdateCatalog(context.Background()); err != nil {
		t.Fatalf("malformed non-authoritative freshness blocked update: %v", err)
	}
	registry, err := manager.readCatalogFreshness()
	if err != nil || len(registry.Repositories) != 1 {
		t.Fatalf("freshness state did not self-heal: %#v, %v", registry, err)
	}
}

func TestSyncSuccessIsNotReplacedByFreshnessWriteFailure(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	catalogPath := filepath.Join(repository, CatalogFilename)
	if err := os.WriteFile(catalogPath, catalogRefreshFixture(t, EmbeddedManifestVersion+7), 0o600); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(t.TempDir(), "data")
	manager := ManifestManager{DataRoot: dataRoot, Repository: repository, AllowUnsigned: true}
	if err := manager.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(manager.freshnessPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	var events []activity.Event
	service := Service{
		DataRoot: dataRoot, Repository: repository,
		Progress: func(event activity.Event) { events = append(events, event) },
	}
	state, err := service.SyncManifest(context.Background(), catalogPath, false)
	if err != nil || state.ActiveVersion != EmbeddedManifestVersion+7 {
		t.Fatalf("sync = %#v, %v", state, err)
	}
	if len(events) != 1 || events[0].Phase != "image-catalog" {
		t.Fatalf("freshness failure was not reported separately: %#v", events)
	}
	if _, current, err := manager.Current(); err != nil || current.ActiveVersion != EmbeddedManifestVersion+7 {
		t.Fatalf("successful sync was not retained: %#v, %v", current, err)
	}
}

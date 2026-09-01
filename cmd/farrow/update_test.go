package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/image"
)

func writeUpdateRepository(t *testing.T, root string, revision uint64) {
	t.Helper()
	catalog := image.EmbeddedCatalog()
	catalog.Version = revision
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, image.CatalogFilename), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCommandRefreshesConfiguredCatalogAndBypassesFreshness(t *testing.T) {
	t.Setenv("FARROW_HOME", filepath.Join(t.TempDir(), "farrow-home"))
	repository := t.TempDir()
	revision := uint64(image.EmbeddedManifestVersion + 1)
	writeUpdateRepository(t, repository, revision)

	for attempt := 0; attempt < 2; attempt++ {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"update", "--repo", repository}, &stdout, &stderr); code != exitOK {
			t.Fatalf("update attempt %d code=%d stdout=%q stderr=%q", attempt+1, code, stdout.String(), stderr.String())
		}
		if stderr.Len() != 0 || !strings.Contains(stdout.String(), repository) || !strings.Contains(stdout.String(), "revision") {
			t.Fatalf("update attempt %d stdout=%q stderr=%q", attempt+1, stdout.String(), stderr.String())
		}
		if attempt == 0 && !strings.Contains(stdout.String(), "updated image catalog") {
			t.Fatalf("first update did not report activation: %q", stdout.String())
		}
		if attempt == 1 && !strings.Contains(stdout.String(), "is current") {
			t.Fatalf("second explicit update did not bypass TTL and report current: %q", stdout.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--json", "update", "--repo", repository}, &stdout, &stderr); code != exitOK {
		t.Fatalf("JSON update code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result image.CatalogRefreshResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON update: %v\n%s", err, stdout.String())
	}
	if !result.Attempted || result.ActiveVersion != revision || result.Repository != repository || result.CheckedAt.IsZero() {
		t.Fatalf("JSON update result = %#v", result)
	}
}

func TestUpdateCommandFailsWhenExplicitRefreshFails(t *testing.T) {
	t.Setenv("FARROW_HOME", filepath.Join(t.TempDir(), "farrow-home"))
	repository := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"update", "--repo", repository}, &stdout, &stderr); code != exitRuntime {
		t.Fatalf("update code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), image.CatalogFilename) {
		t.Fatalf("failed update stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestUpdateCommandContract(t *testing.T) {
	command := newUpdateCommand(&bytes.Buffer{}, &bytes.Buffer{})
	if command.Use != "update" || len(command.Aliases) != 0 {
		t.Fatalf("update command contract = use %q aliases=%v", command.Use, command.Aliases)
	}
	flag := command.LocalNonPersistentFlags().Lookup("repo")
	if flag == nil || flag.Shorthand != "r" || !strings.Contains(command.Long, "does not update the Farrow executable") {
		t.Fatalf("update command help/flags = flag %#v long %q", flag, command.Long)
	}
}

func TestImageListWarnsWhenAutomaticRefreshFallsBack(t *testing.T) {
	t.Setenv("FARROW_HOME", filepath.Join(t.TempDir(), "farrow-home"))
	repository := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"image", "list", "--repo", repository}, &stdout, &stderr); code != exitOK {
		t.Fatalf("image list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "d13") || !strings.Contains(stderr.String(), "Image catalog refresh warning") {
		t.Fatalf("fallback list stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestUpdateSucceedsAndWarnsWhenFreshnessCannotBeWritten(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "farrow-home")
	t.Setenv("FARROW_HOME", dataRoot)
	repository := t.TempDir()
	writeUpdateRepository(t, repository, uint64(image.EmbeddedManifestVersion+8))
	if err := os.MkdirAll(filepath.Join(dataRoot, "images", "manifests", "freshness.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"update", "--repo", repository}, &stdout, &stderr); code != exitOK {
		t.Fatalf("update code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated image catalog") || !strings.Contains(stderr.String(), "freshness state was not recorded") {
		t.Fatalf("freshness-degraded update stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

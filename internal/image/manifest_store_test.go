package image

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aead.dev/minisign"
)

const (
	developmentPrivateRoot1 = "untrusted comment: minisign encrypted secret key\nRWQAAEIyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAO7BJnw0teujbJ9oOq043ZD95Ob7BcsdvNrFUMDvMUw3dfUEnsZAEG0LVheon0eL5PZYT1QNdSvZN+cDhd1F204TzYGJR3ml7AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	developmentPrivateRoot2 = "untrusted comment: minisign encrypted secret key\nRWQAAEIyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASDOx0iSq7RDpxLHyY4Sf8sm1VxTMy8qK17JtEK35cySQAFgg+gXWWxNN6GnxF5GuBvl24Haql+TDHXVH3jqmS+i6XtW5UVS/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

func privateKey(t *testing.T, text string) minisign.PrivateKey {
	t.Helper()
	var key minisign.PrivateKey
	if err := key.UnmarshalText([]byte(text)); err != nil {
		t.Fatal(err)
	}
	return key
}

func signedCatalog(t *testing.T, directory string, version uint64, key minisign.PrivateKey, mutate func(*Catalog)) string {
	t.Helper()
	catalog := EmbeddedCatalog()
	catalog.Version = version
	catalog.GeneratedAt = time.Now().UTC()
	if mutate != nil {
		mutate(&catalog)
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join(directory, "images.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	signature := minisign.SignWithComments(key, data, "timestamp:1787486400", "piglet test manifest")
	if err := os.WriteFile(path+".minisig", signature, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManifestSyncTwoKeysRollbackAndReset(t *testing.T) {
	t.Parallel()
	dataRoot := filepath.Join(t.TempDir(), "data")
	manager := ManifestManager{DataRoot: dataRoot}
	directory1 := t.TempDir()
	path1 := signedCatalog(t, directory1, EmbeddedManifestVersion+2, privateKey(t, developmentPrivateRoot1), nil)
	state, err := manager.Sync(context.Background(), path1, false)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveVersion != EmbeddedManifestVersion+2 || state.KeyID == "" || state.Downgrade {
		t.Fatalf("state = %#v", state)
	}
	catalog, current, err := manager.Current()
	if err != nil || catalog.Version != state.ActiveVersion || current.ActiveDigest != state.ActiveDigest {
		t.Fatalf("current = %#v %#v %v", catalog, current, err)
	}

	directory2 := t.TempDir()
	path2 := signedCatalog(t, directory2, EmbeddedManifestVersion+1, privateKey(t, developmentPrivateRoot2), nil)
	if _, err := manager.Sync(context.Background(), path2, false); err == nil {
		t.Fatal("manifest rollback unexpectedly accepted")
	}
	state, err = manager.Sync(context.Background(), path2, true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Downgrade || state.HighestVersion != EmbeddedManifestVersion+2 || state.ActiveVersion != EmbeddedManifestVersion+1 {
		t.Fatalf("downgrade state = %#v", state)
	}
	reset, err := manager.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reset.Active != "embedded" || reset.HighestVersion != EmbeddedManifestVersion+2 {
		t.Fatalf("reset state = %#v", reset)
	}
}

func TestManifestRejectsTamperUnknownKeyAndEquivocation(t *testing.T) {
	t.Parallel()
	manager := ManifestManager{DataRoot: filepath.Join(t.TempDir(), "data")}
	directory := t.TempDir()
	key := privateKey(t, developmentPrivateRoot1)
	path := signedCatalog(t, directory, EmbeddedManifestVersion+1, key, nil)
	if _, err := manager.Sync(context.Background(), path, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-2] ^= 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Sync(context.Background(), path, false); err == nil {
		t.Fatal("tampered manifest unexpectedly accepted")
	}

	unknownPublic, unknownPrivate, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = unknownPublic
	unknownDir := t.TempDir()
	unknownPath := signedCatalog(t, unknownDir, EmbeddedManifestVersion+2, unknownPrivate, nil)
	if _, err := manager.Sync(context.Background(), unknownPath, false); err == nil {
		t.Fatal("unknown signing key unexpectedly accepted")
	}

	equivocationDir := t.TempDir()
	equivocation := signedCatalog(t, equivocationDir, EmbeddedManifestVersion+1, key, func(catalog *Catalog) {
		artifact := catalog.Images["u24"].Releases["20260801.0.0"]["arm64"]
		artifact.Provenance = "different bytes"
		catalog.Images["u24"].Releases["20260801.0.0"]["arm64"] = artifact
	})
	if _, err := manager.Sync(context.Background(), equivocation, true); err == nil {
		t.Fatal("same-version equivocation unexpectedly accepted")
	}
}

func TestManifestHTTPSSyncAndDowngradeRedirect(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := signedCatalog(t, directory, EmbeddedManifestVersion+3, privateKey(t, developmentPrivateRoot1), nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(path + ".minisig")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, ".minisig") {
			_, _ = writer.Write(signature)
			return
		}
		_, _ = writer.Write(data)
	}))
	defer server.Close()
	manager := ManifestManager{DataRoot: filepath.Join(t.TempDir(), "data"), HTTPClient: server.Client()}
	state, err := manager.Sync(context.Background(), server.URL+"/images.json", false)
	if err != nil || state.ActiveVersion != EmbeddedManifestVersion+3 {
		t.Fatalf("HTTPS sync = %#v, %v", state, err)
	}

	plain := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(data)
	}))
	defer plain.Close()
	redirect := httptest.NewTLSServer(http.RedirectHandler(plain.URL+"/images.json", http.StatusFound))
	defer redirect.Close()
	blocked := ManifestManager{DataRoot: filepath.Join(t.TempDir(), "blocked"), HTTPClient: redirect.Client()}
	if _, err := blocked.Sync(context.Background(), redirect.URL, false); err == nil {
		t.Fatal("HTTPS-to-HTTP manifest redirect unexpectedly accepted")
	}
}

func TestPriorEmbeddedStateAdvancesToNewBinaryBaseline(t *testing.T) {
	t.Parallel()
	manager := ManifestManager{DataRoot: filepath.Join(t.TempDir(), "data")}
	if err := os.MkdirAll(manager.root(), 0o700); err != nil {
		t.Fatal(err)
	}
	prior := ManifestState{
		Schema: ManifestStateSchema, HighestVersion: EmbeddedManifestVersion - 1,
		Active: "embedded", ActiveVersion: EmbeddedManifestVersion - 1,
		ActiveDigest: strings.Repeat("0", 64), Source: "embedded",
		AcceptedAt: embeddedManifestGeneratedAt.Add(-time.Hour),
	}
	data, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.statePath(), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, state, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != EmbeddedManifestVersion || state.ActiveVersion != EmbeddedManifestVersion || state.HighestVersion != EmbeddedManifestVersion || state.ActiveDigest == prior.ActiveDigest {
		t.Fatalf("advanced catalog/state = %#v %#v", catalog, state)
	}
	reset, err := manager.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reset.ActiveVersion != EmbeddedManifestVersion || reset.HighestVersion != EmbeddedManifestVersion {
		t.Fatalf("persisted reset = %#v", reset)
	}
}

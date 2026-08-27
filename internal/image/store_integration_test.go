package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/execx"
)

func makeQCOW2(t *testing.T, path string, backing string) {
	t.Helper()
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Skip("qemu-img is not installed")
	}
	args := []string{"create", "-f", "qcow2"}
	if backing != "" {
		args = append(args, "-F", "qcow2", "-b", backing)
	}
	args = append(args, path, "64M")
	result := exec.Command(qemuImg, args...)
	if output, err := result.CombinedOutput(); err != nil {
		t.Fatalf("create qcow2: %v: %s", err, output)
	}
}

func testImageStore(t *testing.T) Store {
	t.Helper()
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Skip("qemu-img is not installed")
	}
	return Store{DataRoot: filepath.Join(t.TempDir(), "data"), QEMUImg: qemuImg, Runner: execx.OSRunner{Timeout: 10 * time.Second}}
}

func TestIntegrationImportAndValidateCache(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	digest, _, err := digestFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path, metadata, err := store.Import(context.Background(), source, digest)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Digest != digest || metadata.VirtualSize != 64<<20 {
		t.Fatalf("metadata = %#v", metadata)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("cached mode = %v", info.Mode())
	}
	relative, err := filepath.Rel(filepath.Join(store.DataRoot, "images"), path)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{File: filepath.ToSlash(relative), SHA256: digest, Format: "qcow2", ArtifactSize: metadata.ArtifactSize, VirtualSize: metadata.VirtualSize}
	if _, _, err := store.ValidateCached(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationImportRejectsBacking(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "base.qcow2")
	overlay := filepath.Join(dir, "overlay.qcow2")
	makeQCOW2(t, base, "")
	makeQCOW2(t, overlay, base)
	if _, _, err := store.Import(context.Background(), overlay, ""); err == nil {
		t.Fatal("backed image unexpectedly imported")
	}
}

func TestIntegrationHTTPSPull(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = writer.Write(data)
	}))
	defer server.Close()
	store.HTTPClient = server.Client()
	var eventsMu sync.Mutex
	var events []activity.Event
	store.Progress = func(event activity.Event) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}
	entry := Entry{Alias: "test", Release: "1", Arch: "arm64", File: "test/test-1-arm64.qcow2", Upstream: server.URL + "/image.qcow2", SHA256: digest, Format: "qcow2", ArtifactSize: int64(len(data)), VirtualSize: 64 << 20}
	path, _, err := store.Pull(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	var downloaded, verified, ready bool
	for _, event := range events {
		switch event.Phase {
		case "image-download":
			if event.Done && event.Source == server.URL+"/image.qcow2" && event.CurrentBytes == int64(len(data)) && event.TotalBytes == int64(len(data)) {
				downloaded = true
			}
		case "image-verify":
			verified = verified || event.Done
		case "image-ready":
			ready = ready || event.Done
		}
	}
	if !downloaded || !verified || !ready {
		t.Fatalf("progress stages download=%t verify=%t ready=%t events=%+v", downloaded, verified, ready, events)
	}
}

func TestHTTPRepositoryPrecedesUpstreamAndKeepsReadableName(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(data)
	repository := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/u24/u24-1-arm64.qcow2" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = writer.Write(data)
	}))
	defer repository.Close()
	var upstreamHits atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHits.Add(1)
		http.Error(writer, "unexpected upstream request", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	store.Repository = repository.URL
	store.HTTPClient = repository.Client()
	entry := Entry{Alias: "u24", Release: "1", Arch: "arm64", File: "u24/u24-1-arm64.qcow2", Upstream: upstream.URL + "/image.qcow2", SHA256: hex.EncodeToString(digestBytes[:]), Format: "qcow2", ArtifactSize: int64(len(data)), VirtualSize: 64 << 20}
	pathname, metadata, err := store.Pull(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if pathname != filepath.Join(store.DataRoot, "images", "u24", "u24-1-arm64.qcow2") || !strings.HasPrefix(metadata.Source, "repository:") || upstreamHits.Load() != 0 {
		t.Fatalf("repository pull = path %q metadata %#v upstream hits %d", pathname, metadata, upstreamHits.Load())
	}
}

func TestMissingRepositoryArtifactFallsBackToUpstream(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(data)
	repository := httptest.NewServer(http.NotFoundHandler())
	defer repository.Close()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = writer.Write(data)
	}))
	defer upstream.Close()
	store.Repository = repository.URL
	store.HTTPClient = upstream.Client()
	entry := Entry{Alias: "u24", Release: "1", Arch: "arm64", File: "u24/u24-1-arm64.qcow2", Upstream: upstream.URL + "/image.qcow2", SHA256: hex.EncodeToString(digestBytes[:]), Format: "qcow2", ArtifactSize: int64(len(data)), VirtualSize: 64 << 20}
	_, metadata, err := store.Pull(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(metadata.Source, "upstream:") {
		t.Fatalf("fallback metadata = %#v", metadata)
	}
}

func TestHTTPSPullRejectsManifestArtifactSizeMismatch(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = writer.Write(data)
	}))
	defer server.Close()
	store.HTTPClient = server.Client()
	entry := Entry{File: "test/test-1-arm64.qcow2", Upstream: server.URL + "/image.qcow2", SHA256: hex.EncodeToString(digestBytes[:]), Format: "qcow2", ArtifactSize: int64(len(data) + 1)}
	if _, _, err := store.Pull(context.Background(), entry); err == nil {
		t.Fatal("manifest artifact-size mismatch unexpectedly accepted")
	}
}

func TestHTTPSPullRejectsChecksumAndDowngrade(t *testing.T) {
	t.Parallel()
	store := testImageStore(t)
	source := filepath.Join(t.TempDir(), "source.qcow2")
	makeQCOW2(t, source, "")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = writer.Write(data)
	}))
	defer server.Close()
	store.HTTPClient = server.Client()
	badDigest := strings.Repeat("0", 64)
	entry := Entry{File: "test/test-1-arm64.qcow2", Upstream: server.URL + "/image.qcow2", SHA256: badDigest, Format: "qcow2"}
	if _, _, err := store.Pull(context.Background(), entry); err == nil {
		t.Fatal("checksum mismatch unexpectedly accepted")
	}
	badPath, _ := store.Path(entry)
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Fatalf("checksum failure left valid cache path: %v", err)
	}

	plain := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(data)
	}))
	defer plain.Close()
	redirect := httptest.NewTLSServer(http.RedirectHandler(plain.URL+"/image.qcow2", http.StatusFound))
	defer redirect.Close()
	store.HTTPClient = redirect.Client()
	digestBytes := sha256.Sum256(data)
	entry = Entry{File: "test/test-2-arm64.qcow2", Upstream: redirect.URL, SHA256: hex.EncodeToString(digestBytes[:]), Format: "qcow2"}
	if _, _, err := store.Pull(context.Background(), entry); err == nil {
		t.Fatal("HTTPS-to-HTTP redirect unexpectedly accepted")
	}
}

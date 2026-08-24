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
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/execx"
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
	if _, _, err := store.ValidateCached(context.Background(), digest); err != nil {
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
	entry := Entry{Alias: "test", Release: "1", Arch: "arm64", URL: server.URL + "/image.qcow2", SHA256: digest, Format: "qcow2", ArtifactSize: int64(len(data))}
	path, _, err := store.Pull(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
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
	entry := Entry{URL: server.URL + "/image.qcow2", SHA256: hex.EncodeToString(digestBytes[:]), Format: "qcow2", ArtifactSize: int64(len(data) + 1)}
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
	entry := Entry{URL: server.URL + "/image.qcow2", SHA256: badDigest, Format: "qcow2"}
	if _, _, err := store.Pull(context.Background(), entry); err == nil {
		t.Fatal("checksum mismatch unexpectedly accepted")
	}
	if _, err := os.Stat(filepath.Join(store.cacheDir(), badDigest+".qcow2")); !os.IsNotExist(err) {
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
	entry = Entry{URL: redirect.URL, SHA256: hex.EncodeToString(digestBytes[:]), Format: "qcow2"}
	if _, _, err := store.Pull(context.Background(), entry); err == nil {
		t.Fatal("HTTPS-to-HTTP redirect unexpectedly accepted")
	}
}

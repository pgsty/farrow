package setup

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	darwinnet "github.com/pgsty/farrow/internal/network/darwin"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (function doerFunc) Do(request *http.Request) (*http.Response, error) { return function(request) }

func TestDownloadSocketVMNetReleasePublishesVerifiedCache(t *testing.T) {
	t.Parallel()
	const content = "verified archive fixture"
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Length", "24")
		_, _ = io.WriteString(response, content)
	}))
	defer server.Close()
	release := darwinnet.Release{
		Version: "test", Arch: "arm64", ArchiveName: "socket_vmnet-test.tar.gz",
		URL: server.URL + "/socket_vmnet-test.tar.gz", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	verify := func(path, arch string) error {
		if arch != "arm64" {
			return errors.New("wrong architecture")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(data) != content {
			return errors.New("wrong content")
		}
		return nil
	}
	result, err := downloadSocketVMNetRelease(context.Background(), release, "arm64", t.TempDir(), server.Client(), verify)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Downloaded || result.Path == "" {
		t.Fatalf("download result = %#v", result)
	}
	second, err := downloadSocketVMNetRelease(context.Background(), release, "arm64", filepath.Dir(result.Path), doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("cache reuse attempted a request")
	}), verify)
	if err != nil {
		t.Fatal(err)
	}
	if second.Downloaded || second.Path != result.Path {
		t.Fatalf("cached result = %#v", second)
	}
}

func TestDownloadSocketVMNetReleaseRejectsPlainHTTP(t *testing.T) {
	t.Parallel()
	release := darwinnet.Release{
		Version: "test", Arch: "arm64", ArchiveName: "fixture.tar.gz",
		URL: "http://example.invalid/fixture.tar.gz", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	_, err := downloadSocketVMNetRelease(context.Background(), release, "arm64", t.TempDir(), nil, func(string, string) error { return nil })
	if err == nil {
		t.Fatal("plain HTTP release accepted")
	}
}

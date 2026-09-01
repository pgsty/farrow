package private

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pgsty/farrow/internal/image"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/spec"
)

func TestLifecycleCatalogSessionProbesOnceForSevenImages(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	defaultRepository := image.DefaultRepositoryURL
	image.DefaultRepositoryURL = server.URL
	t.Cleanup(func() { image.DefaultRepositoryURL = defaultRepository })
	t.Setenv("FARROW_REPO", "")
	t.Setenv("FARROW_HOME", filepath.Join(t.TempDir(), "farrow-home"))

	manager := Manager{}
	if err := manager.ensureImageSession(context.Background(), image.CatalogRefreshIfDue); err != nil {
		t.Fatal(err)
	}
	resolved := spec.Resolved{Nodes: []spec.Node{
		{Name: "el9", Image: "el9"},
		{Name: "el10", Image: "el10"},
		{Name: "d12", Image: "d12"},
		{Name: "d13", Image: "d13"},
		{Name: "u22", Image: "u22"},
		{Name: "u24", Image: "u24"},
		{Name: "u26", Image: "u26"},
	}}
	profile := platform.Profile{Arch: "arm64", RequiresUEFI: true}
	for attempt := 0; attempt < 2; attempt++ {
		boot, err := manager.resolveBootMode(context.Background(), profile, resolved)
		if err != nil || boot != "uefi" {
			t.Fatalf("resolve boot attempt %d = %q, %v", attempt+1, boot, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("seven image aliases made %d catalog requests, want exactly one failed probe", requests.Load())
	}
	if manager.imageSession == nil || manager.imageSession.Refresh().Warning == "" {
		t.Fatalf("lifecycle session did not retain the offline fallback: %#v", manager.imageSession)
	}
}

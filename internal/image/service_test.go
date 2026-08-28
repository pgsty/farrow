package image

import (
	"context"
	"testing"
)

func TestLookupArchUsesEmbeddedCatalogWithoutDownloading(t *testing.T) {
	t.Parallel()
	service := Service{DataRoot: t.TempDir()}
	entry, err := service.LookupArch(context.Background(), "centos79", "amd64")
	if err != nil || entry.Alias != "el7" || entry.Arch != "amd64" || entry.Boot != "bios" {
		t.Fatalf("EL7 lookup = %#v, %v", entry, err)
	}
	if _, err := service.LookupArch(context.Background(), "el7", "arm64"); err == nil {
		t.Fatal("EL7 arm64 lookup unexpectedly succeeded")
	}
}

package pigsty

import (
	"bytes"
	"os"
	"testing"

	"github.com/pgsty/farrow/internal/profile"
)

func TestPigstyInventoryCorpus(t *testing.T) {
	root := os.Getenv("PIGSTY_SOURCE")
	if root == "" {
		t.Skip("set PIGSTY_SOURCE to opt into the neighboring Pigsty inventory corpus")
	}
	descriptors, err := profile.List()
	if err != nil {
		t.Fatal(err)
	}
	expectedMatches := map[string]int{
		"all": 23, "citus": 28, "deb": 17, "deci": 38, "dual": 6,
		"full": 16, "meta": 5, "minio": 17, "oss": 23, "pro": 23,
		"rpm": 8, "simu": 78, "trio": 19,
	}
	expectedOverlayChanges := map[string]int{"deb": 13, "rpm": 17}
	expectedTuneChanges := map[string]int{
		"all": 1, "citus": 2, "deb": 1, "deci": 0, "dual": 2,
		"full": 2, "meta": 2, "minio": 0, "oss": 1, "pro": 1,
		"rpm": 1, "simu": 0, "trio": 2,
	}
	expectedNoProxyChanges := map[string]int{
		"all": 1, "citus": 0, "deb": 1, "deci": 1, "dual": 0,
		"full": 1, "meta": 1, "minio": 0, "oss": 1, "pro": 1,
		"rpm": 1, "simu": 0, "trio": 1,
	}
	totalMatches := 0
	for _, descriptor := range descriptors {
		descriptor := descriptor
		t.Run(descriptor.Name, func(t *testing.T) {
			baseline, err := Render(root, descriptor.Name, "")
			if err != nil {
				t.Fatal(err)
			}
			source, err := os.ReadFile(baseline.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if descriptor.InventoryMode == profile.InventoryDirect && baseline.TuneChanges == 0 && (!bytes.Equal(source, baseline.Data) || baseline.OverlayChanges != 0) {
				t.Fatal("untuned direct default render is not byte-identical to its bound Pigsty config")
			}
			if descriptor.InventoryMode == profile.InventoryDirect && baseline.TuneChanges != 0 && bytes.Equal(source, baseline.Data) {
				t.Fatal("resource-aware direct render did not materialize tiny tuning")
			}
			if descriptor.InventoryMode == profile.InventoryBuildSubset && (bytes.Equal(source, baseline.Data) || baseline.OverlayChanges == 0) {
				t.Fatal("build-subset default render did not materialize its typed topology overlay")
			}
			if baseline.Replacements != 0 {
				t.Fatal("default render unexpectedly rebased addresses")
			}
			custom, err := Render(root, descriptor.Name, "172.31.251.0/24")
			if err != nil {
				t.Fatal(err)
			}
			if custom.Matches == 0 || custom.Replacements != custom.Matches {
				t.Fatalf("custom result=%#v", custom)
			}
			if custom.Matches != expectedMatches[descriptor.Name] || custom.OverlayChanges != expectedOverlayChanges[descriptor.Name] || custom.TuneChanges != expectedTuneChanges[descriptor.Name] || custom.NoProxyChanges != expectedNoProxyChanges[descriptor.Name] {
				t.Fatalf("custom inventory contract drift: %#v", custom)
			}
			if descriptor.Name == "meta" {
				scaled, err := RenderScaled(root, descriptor.Name, "", 2)
				if err != nil || scaled.Scale != 2 || scaled.TuneChanges != 0 || !bytes.Equal(source, scaled.Data) {
					t.Fatalf("scaled meta tuning contract=%#v err=%v", scaled, err)
				}
			}
			totalMatches += custom.Matches
		})
	}
	if totalMatches < len(descriptors) {
		t.Fatalf("inventory corpus matched only %d address tokens", totalMatches)
	}
}

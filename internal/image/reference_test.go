package image

import "testing"

func TestParseImageReferenceGrammar(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  Reference
	}{
		{"", Reference{}},
		{"d13", Reference{Image: "d13"}},
		{" D13:Testing ", Reference{Image: "d13", Channel: "testing"}},
		{"d13@20260810.2566.0", Reference{Image: "d13", Version: "20260810.2566.0"}},
	}
	for _, test := range tests {
		got, err := ParseReference(test.value)
		if err != nil || got != test.want {
			t.Errorf("ParseReference(%q) = %#v, %v; want %#v", test.value, got, err, test.want)
		}
	}
	for _, value := range []string{"d13:stable@1", "d13:stable:next", "d13/path", "d13@", ":stable"} {
		if _, err := ParseReference(value); err == nil {
			t.Errorf("invalid reference %q accepted", value)
		}
	}
}

func TestCatalogResolvesDefaultChannelExactVersionAndAlias(t *testing.T) {
	t.Parallel()
	catalog := EmbeddedCatalog()
	defaultEntry, err := catalog.Entry("", "arm64")
	if err != nil || defaultEntry.Alias != "u24" || defaultEntry.Channel != "stable" || defaultEntry.Release != "20260801.0.0" {
		t.Fatalf("default entry = %#v, %v", defaultEntry, err)
	}
	exact, err := catalog.Entry("ubuntu2404@20260801.0.0", "arm64")
	if err != nil || exact.Alias != "u24" || exact.Channel != "" || exact.SHA256 != defaultEntry.SHA256 {
		t.Fatalf("exact entry = %#v, %v", exact, err)
	}
	if got, err := CanonicalReference("Ubuntu:stable"); err != nil || got != "u24:stable" {
		t.Fatalf("canonical reference = %q, %v", got, err)
	}
	for _, test := range []struct {
		reference string
		arch      string
		release   string
	}{
		{"el10@10.0.20250609.1", "amd64", "10.0.20250609.1"},
		{"rocky10@10.1.20251116.0", "arm64", "10.1.20251116.0"},
		{"el9@9.3.20231113.0", "amd64", "9.3.20231113.0"},
		{"rocky@9.6.20250531.0", "arm64", "9.6.20250531.0"},
		{"rocky9@9.7.20251123.2", "amd64", "9.7.20251123.2"},
	} {
		entry, err := catalog.Entry(test.reference, test.arch)
		if err != nil || entry.Release != test.release || entry.Channel != "" {
			t.Errorf("exact entry %s/%s = %#v, %v", test.reference, test.arch, entry, err)
		}
	}
	for _, test := range []struct {
		reference string
		arch      string
		release   string
	}{
		{"el9@9", "arm64", "9.8.20260525.0"},
		{"rocky9@9.7", "amd64", "9.7.20251123.2"},
		{"el10@10", "amd64", "10.2.20260525.0"},
		{"rocky10@10.0", "arm64", "10.0.20250609.1"},
	} {
		entry, err := catalog.Entry(test.reference, test.arch)
		if err != nil || entry.Release != test.release || entry.Channel != "" {
			t.Errorf("prefix entry %s/%s = %#v, %v", test.reference, test.arch, entry, err)
		}
	}
}

func TestNumericVersionPrefixSelectsSemanticLatestOnComponentBoundary(t *testing.T) {
	versions := map[string]CatalogVersion{
		"9.7.20251123.1": {},
		"9.7.20251123.2": {},
		"9.8.20260525.0": {},
		"9.10.2.0":       {},
	}
	for selector, want := range map[string]string{
		"9.7.20251123.1": "9.7.20251123.1",
		"9.7":            "9.7.20251123.2",
		"9":              "9.10.2.0",
	} {
		got, err := resolveVersion(versions, selector)
		if err != nil || got != want {
			t.Errorf("resolveVersion(%q) = %q, %v; want %q", selector, got, err, want)
		}
	}
	if got, err := resolveVersion(map[string]CatalogVersion{"9.7.1": {}, "9.70.1": {}}, "9.7"); err != nil || got != "9.7.1" {
		t.Fatalf("component-boundary resolution = %q, %v", got, err)
	}
	for _, selector := range []string{"8", "9.x"} {
		if _, err := resolveVersion(versions, selector); err == nil {
			t.Errorf("invalid/unmatched selector %q was accepted", selector)
		}
	}
}

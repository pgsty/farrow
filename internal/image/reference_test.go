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
	if err != nil || defaultEntry.Alias != "d13" || defaultEntry.Channel != "stable" || defaultEntry.Release != "20260810.2566.0" {
		t.Fatalf("default entry = %#v, %v", defaultEntry, err)
	}
	exact, err := catalog.Entry("debian13@20260810.2566.0", "arm64")
	if err != nil || exact.Alias != "d13" || exact.Channel != "" || exact.SHA256 != defaultEntry.SHA256 {
		t.Fatalf("exact entry = %#v, %v", exact, err)
	}
	if got, err := CanonicalReference("Ubuntu:stable"); err != nil || got != "u24:stable" {
		t.Fatalf("canonical reference = %q, %v", got, err)
	}
}

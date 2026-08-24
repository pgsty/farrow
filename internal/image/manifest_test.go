package image

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type embeddedGolden struct {
	release      string
	url          string
	sha256       string
	artifactSize int64
	virtualSize  int64
	sourceUser   string
}

func TestEmbeddedFormalGuestMatrixExact(t *testing.T) {
	t.Parallel()
	expected := map[string]embeddedGolden{
		"el9/amd64": {
			release: "9.8.20260525.0", url: "https://dl.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base-9.8-20260525.0.x86_64.qcow2",
			sha256: "92c206cc6f790c61583247eefe87890f8828420662c17cacf247cec78ab4eec8", artifactSize: 645988352, virtualSize: 10737418240, sourceUser: "rocky",
		},
		"el9/arm64": {
			release: "9.8.20260525.0", url: "https://dl.rockylinux.org/pub/rocky/9/images/aarch64/Rocky-9-GenericCloud-Base-9.8-20260525.0.aarch64.qcow2",
			sha256: "24692a444f1f0b8bb95375c38c8b43f8099a115347623691be2c330b40c8a1fe", artifactSize: 519831552, virtualSize: 10737418240, sourceUser: "rocky",
		},
		"el10/amd64": {
			release: "10.2.20260525.0", url: "https://dl.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base-10.2-20260525.0.x86_64.qcow2",
			sha256: "9fc9e9ff16888bb68ac39b0392e25c9c92684d50c85f1cce6ab549363bbc4b48", artifactSize: 544997376, virtualSize: 10737418240, sourceUser: "rocky",
		},
		"el10/arm64": {
			release: "10.2.20260525.0", url: "https://dl.rockylinux.org/pub/rocky/10/images/aarch64/Rocky-10-GenericCloud-Base-10.2-20260525.0.aarch64.qcow2",
			sha256: "457c8375e19496f43a25c4a6169fa11237536c53cef6f85a20ea3c5a751aa0f5", artifactSize: 469368832, virtualSize: 10737418240, sourceUser: "rocky",
		},
		"d12/amd64": {
			release: "20260806.2562.0", url: "https://cloud.debian.org/images/cloud/bookworm/20260806-2562/debian-12-generic-amd64-20260806-2562.qcow2",
			sha256: "dd3dbd23a3965318cc9aae32592dcfde4abcb8f90a50ca760a9ca9e8f3ba6255", artifactSize: 448069632, virtualSize: 3221225472, sourceUser: "debian",
		},
		"d12/arm64": {
			release: "20260806.2562.0", url: "https://cloud.debian.org/images/cloud/bookworm/20260806-2562/debian-12-generic-arm64-20260806-2562.qcow2",
			sha256: "8c6b8f81e571d530f6561c707538a4e807de8188c9a3f41af7b52b4e5ed010be", artifactSize: 434044928, virtualSize: 3221225472, sourceUser: "debian",
		},
		"d13/amd64": {
			release: "20260810.2566.0", url: "https://cloud.debian.org/images/cloud/trixie/20260810-2566/debian-13-generic-amd64-20260810-2566.qcow2",
			sha256: "d4e6f5d1e9f571c198a65b45ab1adae6c5734607614e72f9661d84ce5881e5fc", artifactSize: 436404224, virtualSize: 3221225472, sourceUser: "debian",
		},
		"d13/arm64": {
			release: "20260810.2566.0", url: "https://cloud.debian.org/images/cloud/trixie/20260810-2566/debian-13-generic-arm64-20260810-2566.qcow2",
			sha256: "2c546c79ec199983a88e384f6e5d013ab7876353943f7aa614403e3028bbea99", artifactSize: 429195264, virtualSize: 3221225472, sourceUser: "debian",
		},
		"u22/amd64": {
			release: "20260810.0.0", url: "https://cloud-images.ubuntu.com/jammy/20260810/jammy-server-cloudimg-amd64.img",
			sha256: "6de0c42a98dc9a749917dfef34bf54e3595441bf67d39f103a61341560b3da8e", artifactSize: 734344192, virtualSize: 2361393152, sourceUser: "ubuntu",
		},
		"u22/arm64": {
			release: "20260810.0.0", url: "https://cloud-images.ubuntu.com/jammy/20260810/jammy-server-cloudimg-arm64.img",
			sha256: "b57a88a8d3b9f33d48f1b3d70a1aac7ae79760c9b507699d2601989eadac02b1", artifactSize: 703484928, virtualSize: 2361393152, sourceUser: "ubuntu",
		},
		"u24/amd64": {
			release: "20260801.0.0", url: "https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-amd64.img",
			sha256: "0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe", artifactSize: 624239616, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
		"u24/arm64": {
			release: "20260801.0.0", url: "https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-arm64.img",
			sha256: "aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476", artifactSize: 618417664, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
		"u26/amd64": {
			release: "20260731.0.0", url: "https://cloud-images.ubuntu.com/resolute/20260731/resolute-server-cloudimg-amd64.img",
			sha256: "9dc7c5363c0146a08ba0c9aa834d82c2c6dfbb1c471ad9a2f0aba1189e21be05", artifactSize: 860447744, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
		"u26/arm64": {
			release: "20260731.0.0", url: "https://cloud-images.ubuntu.com/resolute/20260731/resolute-server-cloudimg-arm64.img",
			sha256: "3e113fdd41f39e13729375173bb2ae793f87dc6db4294e5251ff2476971788ba", artifactSize: 940920832, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
	}

	if len(expected) != 14 || len(embedded) != len(expected) {
		t.Fatalf("formal matrix sizes: golden=%d embedded=%d", len(expected), len(embedded))
	}
	entries := EmbeddedEntries()
	if len(entries) != len(expected) {
		t.Fatalf("EmbeddedEntries count = %d, want %d", len(entries), len(expected))
	}

	wantOrder := make([]string, 0, 14)
	for _, alias := range formalAliases {
		for _, arch := range []string{"amd64", "arm64"} {
			wantOrder = append(wantOrder, alias+"/"+arch)
		}
	}
	gotOrder := make([]string, 0, len(entries))
	perAlias := make(map[string]map[string]bool)
	for _, entry := range entries {
		key := entry.Alias + "/" + entry.Arch
		gotOrder = append(gotOrder, key)
		want, ok := expected[key]
		if !ok {
			t.Errorf("unexpected embedded entry %s", key)
			continue
		}
		if entry.Release != want.release || entry.URL != want.url || entry.SHA256 != want.sha256 || entry.ArtifactSize != want.artifactSize || entry.VirtualSize != want.virtualSize || entry.SourceUser != want.sourceUser {
			t.Errorf("%s metadata mismatch:\n got %#v\nwant %#v", key, entry, want)
		}
		if entry.Format != "qcow2" || entry.Boot != "uefi" || entry.Status != "testing" || strings.TrimSpace(entry.Provenance) == "" {
			t.Errorf("%s incomplete policy fields: %#v", key, entry)
		}
		parsed, err := url.Parse(entry.URL)
		if err != nil || hasMovingReleasePath(parsed.Path) || strings.Contains(strings.ToLower(entry.URL), "latest") {
			t.Errorf("%s has a moving or invalid URL: %q", key, entry.URL)
		}
		if parsed.Host != "dl.rockylinux.org" && parsed.Host != "cloud.debian.org" && parsed.Host != "cloud-images.ubuntu.com" {
			t.Errorf("%s is not on an expected distribution-owned host: %q", key, parsed.Host)
		}
		if entry.ArtifactSize <= 0 || entry.VirtualSize <= 0 || entry.ArtifactSize > entry.VirtualSize {
			t.Errorf("%s has invalid byte sizes: %#v", key, entry)
		}
		if perAlias[entry.Alias] == nil {
			perAlias[entry.Alias] = make(map[string]bool)
		}
		perAlias[entry.Alias][entry.Arch] = true
		resolved, err := Embedded(entry.Alias, entry.Arch)
		if err != nil || !reflect.DeepEqual(resolved, entry) {
			t.Errorf("Embedded(%s, %s) = %#v, %v", entry.Alias, entry.Arch, resolved, err)
		}
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Errorf("EmbeddedEntries order = %v, want %v", gotOrder, wantOrder)
	}
	for _, alias := range formalAliases {
		arches := perAlias[alias]
		if len(arches) != 2 || !arches["amd64"] || !arches["arm64"] {
			t.Errorf("%s architecture set = %v", alias, arches)
		}
	}
}

func TestEmbeddedFriendlyAliases(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"rocky9": "el9", "rocky": "el9", "rocky10": "el10",
		"debian12": "d12", "bookworm": "d12", "debian13": "d13", "debian": "d13", "trixie": "d13",
		"ubuntu22": "u22", "ubuntu2204": "u22", "jammy": "u22",
		"ubuntu": "u24", "ubuntu24": "u24", "ubuntu2404": "u24", "noble": "u24",
		"ubuntu26": "u26", "ubuntu2604": "u26", "resolute": "u26",
	}
	for alias, canonical := range cases {
		if got := CanonicalAlias("  " + strings.ToUpper(alias) + "  "); got != canonical {
			t.Errorf("CanonicalAlias(%q) = %q, want %q", alias, got, canonical)
		}
		entry, err := Embedded(alias, "amd64")
		if err != nil || entry.Alias != canonical || entry.Arch != "amd64" {
			t.Errorf("Embedded(%q, amd64) = %#v, %v", alias, entry, err)
		}
	}
	if _, err := Embedded("unknown", "amd64"); err == nil {
		t.Fatal("unknown alias unexpectedly resolved")
	}
	for _, alias := range []string{"el8", "rocky8"} {
		if _, err := Embedded(alias, "amd64"); err == nil {
			t.Fatalf("retired alias %q unexpectedly resolved", alias)
		}
	}
	if _, err := Embedded("u24", "s390x"); err == nil {
		t.Fatal("unsupported architecture unexpectedly resolved")
	}
}

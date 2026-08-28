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
		"el7/amd64": {
			release: "7.9.20221112.0", url: "https://cloud.centos.org/centos/7/images/CentOS-7-x86_64-GenericCloud-2211.qcow2",
			sha256: "284aab2b23d91318f169ff464bce4d53404a15a0618ceb34562838c59af4adea", artifactSize: 902889472, virtualSize: 8589934592, sourceUser: "centos",
		},
		"el8/amd64": {
			release: "8.10.20240528.0", url: "https://dl.rockylinux.org/pub/rocky/8/images/x86_64/Rocky-8-GenericCloud-Base-8.10-20240528.0.x86_64.qcow2",
			sha256: "e56066c58606191e96184de9a9183a3af33c59bcbd8740d8b10ca054a7a89c14", artifactSize: 2065760256, virtualSize: 10737418240, sourceUser: "rocky",
		},
		"el8/arm64": {
			release: "8.10.20240528.0", url: "https://dl.rockylinux.org/pub/rocky/8/images/aarch64/Rocky-8-GenericCloud-Base-8.10-20240528.0.aarch64.qcow2",
			sha256: "946b5b9845aa5e3ed98f1bc6ee9873201712a2aef01b87731aed16857e0ca13f", artifactSize: 1925644288, virtualSize: 10737418240, sourceUser: "rocky",
		},
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

	if len(expected) != 17 || len(embedded) != len(expected) {
		t.Fatalf("formal matrix sizes: golden=%d embedded=%d", len(expected), len(embedded))
	}
	entries := EmbeddedEntries()
	if len(entries) != len(expected) {
		t.Fatalf("EmbeddedEntries count = %d, want %d", len(entries), len(expected))
	}

	wantOrder := append([]string(nil), formalMatrix...)
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
		if entry.Release != want.release || entry.Upstream != want.url || entry.SHA256 != want.sha256 || entry.ArtifactSize != want.artifactSize || entry.VirtualSize != want.virtualSize || entry.SourceUser != want.sourceUser {
			t.Errorf("%s metadata mismatch:\n got %#v\nwant %#v", key, entry, want)
		}
		wantBoot, wantStatus := "uefi", "testing"
		if entry.Alias == "el7" {
			wantBoot, wantStatus = "bios", "deprecated"
		}
		if entry.Format != "qcow2" || entry.Boot != wantBoot || entry.Status != wantStatus || strings.TrimSpace(entry.Provenance) == "" {
			t.Errorf("%s incomplete policy fields: %#v", key, entry)
		}
		parsed, err := url.Parse(entry.Upstream)
		if err != nil || hasMovingReleasePath(parsed.Path) || strings.Contains(strings.ToLower(entry.Upstream), "latest") {
			t.Errorf("%s has a moving or invalid URL: %q", key, entry.Upstream)
		}
		if parsed.Host != "dl.rockylinux.org" && parsed.Host != "cloud.debian.org" && parsed.Host != "cloud-images.ubuntu.com" && parsed.Host != "cloud.centos.org" {
			t.Errorf("%s is not on an expected distribution-owned host: %q", key, parsed.Host)
		}
		if entry.ArtifactSize <= 0 || entry.VirtualSize <= 0 || entry.ArtifactSize > entry.VirtualSize {
			t.Errorf("%s has invalid byte sizes: %#v", key, entry)
		}
		if perAlias[entry.Alias] == nil {
			perAlias[entry.Alias] = make(map[string]bool)
		}
		perAlias[entry.Alias][entry.Arch] = true
		resolved, err := embeddedEntry(entry.Alias, entry.Arch)
		if err != nil || !reflect.DeepEqual(resolved, entry) {
			t.Errorf("Embedded(%s, %s) = %#v, %v", entry.Alias, entry.Arch, resolved, err)
		}
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Errorf("EmbeddedEntries order = %v, want %v", gotOrder, wantOrder)
	}
	if arches := perAlias["el7"]; len(arches) != 1 || !arches["amd64"] {
		t.Errorf("el7 architecture set = %v", arches)
	}
	for _, alias := range formalAliases {
		if alias == "el7" {
			continue
		}
		arches := perAlias[alias]
		if len(arches) != 2 || !arches["amd64"] || !arches["arm64"] {
			t.Errorf("%s architecture set = %v", alias, arches)
		}
	}
}

func TestEmbeddedFriendlyAliases(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"c7": "el7", "centos7": "el7", "centos79": "el7",
		"rocky8": "el8",
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
		entry, err := embeddedEntry(alias, "amd64")
		if err != nil || entry.Alias != canonical || entry.Arch != "amd64" {
			t.Errorf("Embedded(%q, amd64) = %#v, %v", alias, entry, err)
		}
	}
	if _, err := embeddedEntry("unknown", "amd64"); err == nil {
		t.Fatal("unknown alias unexpectedly resolved")
	}
	if _, err := embeddedEntry("el7", "arm64"); err == nil {
		t.Fatal("EL7 unexpectedly has an arm64 artifact")
	}
	if _, err := embeddedEntry("u24", "s390x"); err == nil {
		t.Fatal("unsupported architecture unexpectedly resolved")
	}
}

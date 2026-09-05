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

func embeddedEntries(t *testing.T) []Entry {
	t.Helper()
	catalog := EmbeddedCatalog()
	entries := make([]Entry, 0, len(formalMatrix))
	for _, key := range formalMatrix {
		imageName, arch, _ := strings.Cut(key, "/")
		entry, err := catalog.Entry(imageName, arch)
		if err != nil {
			t.Fatalf("invalid embedded image matrix entry %s: %v", key, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestEmbeddedFormalGuestMatrixExact(t *testing.T) {
	t.Parallel()
	expected := map[string]embeddedGolden{
		"el7/amd64": {
			release: "7.9.20221112.0", url: "https://cloud.centos.org/centos/7/images/CentOS-7-x86_64-GenericCloud-2211.qcow2",
			sha256: "284aab2b23d91318f169ff464bce4d53404a15a0618ceb34562838c59af4adea", artifactSize: 902889472, virtualSize: 8589934592, sourceUser: "centos",
		},
		"el8/amd64": {
			release: "8.10.20240528.1", url: "",
			sha256: "8010643eeb7bca72287165127422b3c2ace3b1e942dd531fa23a2bd9db62a699", artifactSize: 2081488896, virtualSize: 10737418240, sourceUser: "dba",
		},
		"el8/arm64": {
			release: "8.10.20240528.1", url: "",
			sha256: "b440c55b9d6e98fe58bfb9a66f52d82299cff1dc97a7fa2530b30b45eefa447d", artifactSize: 1941438464, virtualSize: 10737418240, sourceUser: "dba",
		},
		"el9/amd64": {
			release: "9.8.20260525.1", url: "",
			sha256: "b6410ae2c0dee331410680c0b13619f8a9f512560fbf641ab8c495e781b0448d", artifactSize: 655687680, virtualSize: 10737418240, sourceUser: "dba",
		},
		"el9/arm64": {
			release: "9.8.20260525.1", url: "",
			sha256: "8203af2032444144cfefddebf710ea0fd467c3022544f849dd6e12bc5ee0799c", artifactSize: 529727488, virtualSize: 10737418240, sourceUser: "dba",
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
			release: "20260806.2562.1", url: "",
			sha256: "5e25ae70d8b95a1f1258c9243c8130e746dae602185a97c186ddbe62f5c563a1", artifactSize: 766050304, virtualSize: 3221225472, sourceUser: "dba",
		},
		"d12/arm64": {
			release: "20260806.2562.1", url: "",
			sha256: "a8898040189bf0e0d7dca6cf8db965957fbfb172c726e6ba264fe18303f48607", artifactSize: 711196672, virtualSize: 3221225472, sourceUser: "dba",
		},
		"d13/amd64": {
			release: "20260810.2566.1", url: "",
			sha256: "1abcb1ee7081ae5f577d25f573376d140e2a15df8bfe135418f7d9999ce4bab5", artifactSize: 533135360, virtualSize: 3221225472, sourceUser: "dba",
		},
		"d13/arm64": {
			release: "20260810.2566.1", url: "",
			sha256: "a195ba0b47a932c07934eda13500581962f419dc4ce78b22027281efe9ec3385", artifactSize: 575537152, virtualSize: 3221225472, sourceUser: "dba",
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

	entries := embeddedEntries(t)
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
		wantBoot, wantStatus := "uefi", "supported"
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
		if entry.Upstream != "" && parsed.Host != "dl.rockylinux.org" && parsed.Host != "cloud.debian.org" && parsed.Host != "cloud-images.ubuntu.com" && parsed.Host != "cloud.centos.org" {
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
		if err != nil || resolved.SHA256 != entry.SHA256 || resolved.Release != entry.Release || resolved.Arch != entry.Arch {
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

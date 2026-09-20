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
			release: "20260909.2596.1", url: "",
			sha256: "ad255513c30684f7bc833ba8aaa55745764d957c2ea587ef28adf78548dfcfbf", artifactSize: 767492096, virtualSize: 3221225472, sourceUser: "dba",
		},
		"d12/arm64": {
			release: "20260909.2596.1", url: "",
			sha256: "b4095d161fd3b551df4cc47db9019cb98b96c45ad3320a00c6ef9ca3425ca676", artifactSize: 750583808, virtualSize: 3221225472, sourceUser: "dba",
		},
		"d13/amd64": {
			release: "20260914.2601.1", url: "",
			sha256: "3ec9fc9ca0adca84dbdcf9297687e1f0b893138ed4205d6584b47ecda4f76464", artifactSize: 532873216, virtualSize: 3221225472, sourceUser: "dba",
		},
		"d13/arm64": {
			release: "20260914.2601.1", url: "",
			sha256: "9fe2f0a4fe6a43ff04f741d41962f1107b840c14b80f23b7c967a731e0fe1e6e", artifactSize: 582090752, virtualSize: 3221225472, sourceUser: "dba",
		},
		"u22/amd64": {
			release: "20260913.0.0", url: "https://cloud-images.ubuntu.com/releases/jammy/release-20260913/ubuntu-22.04-server-cloudimg-amd64.img",
			sha256: "9144540e8af7637d258b50dbabe82ce1aa6752c9574fedfb048270da0e087899", artifactSize: 735388672, virtualSize: 2361393152, sourceUser: "ubuntu",
		},
		"u22/arm64": {
			release: "20260913.0.0", url: "https://cloud-images.ubuntu.com/releases/jammy/release-20260913/ubuntu-22.04-server-cloudimg-arm64.img",
			sha256: "ab5fcc80611a98bf999018045119d87b3a0e7c78f3b43b254b93d5c22bae3ff6", artifactSize: 704972800, virtualSize: 2361393152, sourceUser: "ubuntu",
		},
		"u24/amd64": {
			release: "20260911.0.0", url: "https://cloud-images.ubuntu.com/releases/noble/release-20260911/ubuntu-24.04-server-cloudimg-amd64.img",
			sha256: "612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354", artifactSize: 625256960, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
		"u24/arm64": {
			release: "20260911.0.0", url: "https://cloud-images.ubuntu.com/releases/noble/release-20260911/ubuntu-24.04-server-cloudimg-arm64.img",
			sha256: "7b682958a67ff5de068e36de6af8b75fa645d296af5a70d6500527f6a33781db", artifactSize: 619621888, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
		"u26/amd64": {
			release: "20260918.0.0", url: "https://cloud-images.ubuntu.com/releases/resolute/release-20260918/ubuntu-26.04-server-cloudimg-amd64.img",
			sha256: "4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc", artifactSize: 864411136, virtualSize: 3758096384, sourceUser: "ubuntu",
		},
		"u26/arm64": {
			release: "20260918.0.0", url: "https://cloud-images.ubuntu.com/releases/resolute/release-20260918/ubuntu-26.04-server-cloudimg-arm64.img",
			sha256: "8dc812bc6356d0abf825d8029f25f1b71f02cb103e1d0cc5c17fbb2572322972", artifactSize: 944823296, virtualSize: 3758096384, sourceUser: "ubuntu",
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

// Package image owns the signed image catalog and the local image store.
package image

import (
	"fmt"
	"strings"
)

type Entry struct {
	Alias        string `json:"alias"`
	Release      string `json:"release"`
	Arch         string `json:"arch"`
	File         string `json:"file"`
	Upstream     string `json:"upstream"`
	SHA256       string `json:"sha256"`
	Format       string `json:"format"`
	ArtifactSize int64  `json:"artifact_size"`
	VirtualSize  int64  `json:"virtual_size"`
	SourceUser   string `json:"source_user"`
	Boot         string `json:"boot"`
	Status       string `json:"status"`
	Provenance   string `json:"provenance"`
}

// These distribution-owned, release-pinned sources are testing inputs until
// Farrow has owner-provided image hosting/signing custody and every alias/arch
// has passed the required native smoke matrix.
var embedded = map[string]Entry{
	"el9/amd64": {
		Alias: "el9", Release: "9.8.20260525.0", Arch: "amd64",
		Upstream:     "https://dl.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base-9.8-20260525.0.x86_64.qcow2",
		SHA256:       "92c206cc6f790c61583247eefe87890f8828420662c17cacf247cec78ab4eec8",
		Format:       "qcow2",
		ArtifactSize: 645988352,
		VirtualSize:  10737418240,
		SourceUser:   "rocky", Boot: "uefi", Status: "testing",
		Provenance: "Rocky Linux 9.8 GenericCloud Base 20260525.0; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"el9/arm64": {
		Alias: "el9", Release: "9.8.20260525.0", Arch: "arm64",
		Upstream:     "https://dl.rockylinux.org/pub/rocky/9/images/aarch64/Rocky-9-GenericCloud-Base-9.8-20260525.0.aarch64.qcow2",
		SHA256:       "24692a444f1f0b8bb95375c38c8b43f8099a115347623691be2c330b40c8a1fe",
		Format:       "qcow2",
		ArtifactSize: 519831552,
		VirtualSize:  10737418240,
		SourceUser:   "rocky", Boot: "uefi", Status: "testing",
		Provenance: "Rocky Linux 9.8 GenericCloud Base 20260525.0; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"el10/amd64": {
		Alias: "el10", Release: "10.2.20260525.0", Arch: "amd64",
		Upstream:     "https://dl.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base-10.2-20260525.0.x86_64.qcow2",
		SHA256:       "9fc9e9ff16888bb68ac39b0392e25c9c92684d50c85f1cce6ab549363bbc4b48",
		Format:       "qcow2",
		ArtifactSize: 544997376,
		VirtualSize:  10737418240,
		SourceUser:   "rocky", Boot: "uefi", Status: "testing",
		Provenance: "Rocky Linux 10.2 GenericCloud Base 20260525.0; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"el10/arm64": {
		Alias: "el10", Release: "10.2.20260525.0", Arch: "arm64",
		Upstream:     "https://dl.rockylinux.org/pub/rocky/10/images/aarch64/Rocky-10-GenericCloud-Base-10.2-20260525.0.aarch64.qcow2",
		SHA256:       "457c8375e19496f43a25c4a6169fa11237536c53cef6f85a20ea3c5a751aa0f5",
		Format:       "qcow2",
		ArtifactSize: 469368832,
		VirtualSize:  10737418240,
		SourceUser:   "rocky", Boot: "uefi", Status: "testing",
		Provenance: "Rocky Linux 10.2 GenericCloud Base 20260525.0; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"d12/amd64": {
		Alias: "d12", Release: "20260806.2562.0", Arch: "amd64",
		Upstream:     "https://cloud.debian.org/images/cloud/bookworm/20260806-2562/debian-12-generic-amd64-20260806-2562.qcow2",
		SHA256:       "dd3dbd23a3965318cc9aae32592dcfde4abcb8f90a50ca760a9ca9e8f3ba6255",
		Format:       "qcow2",
		ArtifactSize: 448069632,
		VirtualSize:  3221225472,
		SourceUser:   "debian", Boot: "uefi", Status: "testing",
		Provenance: "Debian 12 generic cloud image 20260806-2562; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"d12/arm64": {
		Alias: "d12", Release: "20260806.2562.0", Arch: "arm64",
		Upstream:     "https://cloud.debian.org/images/cloud/bookworm/20260806-2562/debian-12-generic-arm64-20260806-2562.qcow2",
		SHA256:       "8c6b8f81e571d530f6561c707538a4e807de8188c9a3f41af7b52b4e5ed010be",
		Format:       "qcow2",
		ArtifactSize: 434044928,
		VirtualSize:  3221225472,
		SourceUser:   "debian", Boot: "uefi", Status: "testing",
		Provenance: "Debian 12 generic cloud image 20260806-2562; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"d13/amd64": {
		Alias: "d13", Release: "20260810.2566.0", Arch: "amd64",
		Upstream:     "https://cloud.debian.org/images/cloud/trixie/20260810-2566/debian-13-generic-amd64-20260810-2566.qcow2",
		SHA256:       "d4e6f5d1e9f571c198a65b45ab1adae6c5734607614e72f9661d84ce5881e5fc",
		Format:       "qcow2",
		ArtifactSize: 436404224,
		VirtualSize:  3221225472,
		SourceUser:   "debian", Boot: "uefi", Status: "testing",
		Provenance: "Debian 13 generic cloud image 20260810-2566; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"d13/arm64": {
		Alias: "d13", Release: "20260810.2566.0", Arch: "arm64",
		Upstream:     "https://cloud.debian.org/images/cloud/trixie/20260810-2566/debian-13-generic-arm64-20260810-2566.qcow2",
		SHA256:       "2c546c79ec199983a88e384f6e5d013ab7876353943f7aa614403e3028bbea99",
		Format:       "qcow2",
		ArtifactSize: 429195264,
		VirtualSize:  3221225472,
		SourceUser:   "debian", Boot: "uefi", Status: "testing",
		Provenance: "Debian 13 generic cloud image 20260810-2566; distribution-owned dated artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"u22/amd64": {
		Alias: "u22", Release: "20260810.0.0", Arch: "amd64",
		Upstream:     "https://cloud-images.ubuntu.com/jammy/20260810/jammy-server-cloudimg-amd64.img",
		SHA256:       "6de0c42a98dc9a749917dfef34bf54e3595441bf67d39f103a61341560b3da8e",
		Format:       "qcow2",
		ArtifactSize: 734344192,
		VirtualSize:  2361393152,
		SourceUser:   "ubuntu", Boot: "uefi", Status: "testing",
		Provenance: "Canonical Ubuntu Server 22.04 dated cloud image 20260810; distribution-owned artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"u22/arm64": {
		Alias: "u22", Release: "20260810.0.0", Arch: "arm64",
		Upstream:     "https://cloud-images.ubuntu.com/jammy/20260810/jammy-server-cloudimg-arm64.img",
		SHA256:       "b57a88a8d3b9f33d48f1b3d70a1aac7ae79760c9b507699d2601989eadac02b1",
		Format:       "qcow2",
		ArtifactSize: 703484928,
		VirtualSize:  2361393152,
		SourceUser:   "ubuntu", Boot: "uefi", Status: "testing",
		Provenance: "Canonical Ubuntu Server 22.04 dated cloud image 20260810; distribution-owned artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"u24/amd64": {
		Alias: "u24", Release: "20260801.0.0", Arch: "amd64",
		Upstream:     "https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-amd64.img",
		SHA256:       "0533b0655c32e68b31d792ecd6ccfca95abdbc536c4446874fe0513bd4140ffe",
		Format:       "qcow2",
		ArtifactSize: 624239616,
		VirtualSize:  3758096384,
		SourceUser:   "ubuntu", Boot: "uefi", Status: "testing",
		Provenance: "Canonical Ubuntu Server 24.04 dated cloud image 20260801; distribution-owned artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"u24/arm64": {
		Alias: "u24", Release: "20260801.0.0", Arch: "arm64",
		Upstream:     "https://cloud-images.ubuntu.com/noble/20260801/noble-server-cloudimg-arm64.img",
		SHA256:       "aa6da05756e85ea6dde4836b841fecb10cfd1ba3bcea320189d9af945db70476",
		Format:       "qcow2",
		ArtifactSize: 618417664,
		VirtualSize:  3758096384,
		SourceUser:   "ubuntu", Boot: "uefi", Status: "testing",
		Provenance: "Canonical Ubuntu Server 24.04 dated cloud image 20260801; distribution-owned artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"u26/amd64": {
		Alias: "u26", Release: "20260731.0.0", Arch: "amd64",
		Upstream:     "https://cloud-images.ubuntu.com/resolute/20260731/resolute-server-cloudimg-amd64.img",
		SHA256:       "9dc7c5363c0146a08ba0c9aa834d82c2c6dfbb1c471ad9a2f0aba1189e21be05",
		Format:       "qcow2",
		ArtifactSize: 860447744,
		VirtualSize:  3758096384,
		SourceUser:   "ubuntu", Boot: "uefi", Status: "testing",
		Provenance: "Canonical Ubuntu Server 26.04 dated cloud image 20260731; distribution-owned artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
	"u26/arm64": {
		Alias: "u26", Release: "20260731.0.0", Arch: "arm64",
		Upstream:     "https://cloud-images.ubuntu.com/resolute/20260731/resolute-server-cloudimg-arm64.img",
		SHA256:       "3e113fdd41f39e13729375173bb2ae793f87dc6db4294e5251ff2476971788ba",
		Format:       "qcow2",
		ArtifactSize: 940920832,
		VirtualSize:  3758096384,
		SourceUser:   "ubuntu", Boot: "uefi", Status: "testing",
		Provenance: "Canonical Ubuntu Server 26.04 dated cloud image 20260731; distribution-owned artifact; local bytes, SHA-256, qcow2 metadata, and EFI system partition verified 2026-08-24",
	},
}

var aliases = map[string]string{
	"rocky9": "el9", "rocky": "el9",
	"rocky10":  "el10",
	"debian12": "d12", "bookworm": "d12",
	"debian13": "d13", "debian": "d13",
	"trixie":   "d13",
	"ubuntu22": "u22", "ubuntu2204": "u22", "jammy": "u22",
	"ubuntu": "u24", "ubuntu24": "u24", "ubuntu2404": "u24", "noble": "u24",
	"ubuntu26": "u26", "ubuntu2604": "u26", "resolute": "u26",
}

var formalAliases = []string{"el9", "el10", "d12", "d13", "u22", "u24", "u26"}

func CanonicalAlias(alias string) string {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if canonical, ok := aliases[alias]; ok {
		alias = canonical
	}
	return alias
}

func EmbeddedEntries() []Entry {
	entries := make([]Entry, 0, len(formalAliases)*2)
	for _, alias := range formalAliases {
		for _, arch := range []string{"amd64", "arm64"} {
			entries = append(entries, withRepositoryFile(embedded[alias+"/"+arch]))
		}
	}
	return entries
}

func withRepositoryFile(entry Entry) Entry {
	entry.File = fmt.Sprintf("%s/%s-%s-%s.qcow2", entry.Alias, entry.Alias, entry.Release, entry.Arch)
	return entry
}

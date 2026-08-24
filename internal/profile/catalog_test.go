package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pgsty/piglet/internal/config"
	"github.com/pgsty/piglet/internal/spec"
	profileassets "github.com/pgsty/piglet/profiles"
)

var expectedProfileNodeCounts = map[string]int{
	"all": 7, "citus": 13, "deb": 5, "deci": 10, "dual": 2,
	"full": 4, "meta": 1, "minio": 4, "oss": 7, "pro": 7,
	"rpm": 2, "simu": 20, "trio": 3,
}

var expectedProfileInventoryRefs = map[string]string{
	"all": "conf/build/oss.yml", "citus": "conf/ha/citus.yml",
	"deb": "conf/build/oss.yml", "deci": "conf/ha/octo.yml",
	"dual": "conf/ha/dual.yml", "full": "conf/ha/full.yml",
	"meta": "conf/meta.yml", "minio": "conf/demo/minio.yml",
	"oss": "conf/build/oss.yml", "pro": "conf/build/oss.yml",
	"rpm": "conf/build/oss.yml", "simu": "conf/ha/simu.yml",
	"trio": "conf/ha/trio.yml",
}

var expectedProfileSHA256 = map[string]string{
	"all":   "6d9097a5ee1b684585984ad26812bd500af9beff4b33b59d54762b3920f1224b",
	"citus": "3c5a29c821d295ce79112830dd769ff429954bbce539c24d05a6174eac4eff03",
	"deb":   "65b6d3bdb214433d2df1d277c240be3b1f732542c4eb6d490b917f6c75947830",
	"deci":  "553e261524ff2684cb490c45f678cc873f3ee98a4af1f71b0aa57985f9f64cb8",
	"dual":  "ec4bbceb689335eea45031a9aaad6d5f69c8450b09ae1bd7d5e3d60161ef4289",
	"full":  "912fea61bf1602c2a437570561a6ab4d0a5a8c152695147ad9e96ae831bc9336",
	"meta":  "441eedd8aa3066e8018255a4f885648c53fa76d582c59047d79e9a0abf1d75a9",
	"minio": "73fb74903787aa6dd5b3f955d56c11aa4235a57ac70c705b8e88785ff11246c3",
	"oss":   "ef885d71e3175c12206ceb491e7c7b02bf5072febd6802e9751b8e0985971fa8",
	"pro":   "11f7740644abbaacce476800585833a4f9121aadf6c4c4556c43588da51e2a85",
	"rpm":   "d0b0244ad6bc62e4a11b9b9c63c9ef2f5137597b8ce4d4665341ebbe98ecab4f",
	"simu":  "ff3299eb874761498372f419ef7afff9dd268d2e60de0cd80a9892b8dfe2a05c",
	"trio":  "34f7c4f3ba7810728b19291c4a332e464c6783d17872359e28773240a6b4be43",
}

var expectedResolvedSHA256 = map[string]string{
	"all":   "69933516fd7bd0b930a72d3d48c69a662e4966a9a2f212d21eca9db134985458",
	"citus": "a26b00635a7cb408e9a9c4f49c305d8dcf7758d1a4e87db7e3fdd45fa2b8afd6",
	"deb":   "6ad3ee5d8741a5ca0803f0789da4bd8aabe3ca923d2d6f7734c1f3e1ce98002b",
	"deci":  "62cd1bc01928d2cd0ecc2ef163f38e71a5ede4238f826faf30dfcb70ef63f46c",
	"dual":  "c199fd4ebf3f91d33c46eea4d2365e927c6be7cacb0e247d2f7fabaeacd4c5dd",
	"full":  "c3cd12da8c9fefda13f59be88ab3c663b20f8e53b5b07452f3b7436e0d8b2638",
	"meta":  "63e9fbd5ad1fb7272b61d406612262abc17739961314092903c6c3f02b16ab92",
	"minio": "02d4053268206eaff3e0f2cf16c9a9582cb516ffeb5ec33c2cade858e9ae4f52",
	"oss":   "3076e29202eae80c58e5ff66c9a6f38b38077d03b5b83ab34ebdd2ae04fc0089",
	"pro":   "853294fa3bbfc9e3e225462761bb2c4b536efe89b2b02c286eeacd941f561a58",
	"rpm":   "4cfec356edc4ff9929846649f7eb8a68a44804dba6aea0299bb8291045b7991d",
	"simu":  "9de9b7c0e9e8a8ddc33578f6ffbc7a8e9a9293122ebd06ce61a66559b6832b5f",
	"trio":  "bc570d0bfaceb161701eed5aa26187b7a436be9b189f322c481c93b66709b601",
}

func TestCatalogHasExactPigletSet(t *testing.T) {
	t.Parallel()
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != CatalogSchema {
		t.Fatalf("catalog schema = %d, want %d", catalog.Schema, CatalogSchema)
	}
	got := make([]string, 0, len(catalog.Profiles))
	for _, descriptor := range catalog.Profiles {
		got = append(got, descriptor.Name)
		if descriptor.InventoryRef != expectedProfileInventoryRefs[descriptor.Name] {
			t.Errorf("profile %s inventory ref = %q, want %q", descriptor.Name, descriptor.InventoryRef, expectedProfileInventoryRefs[descriptor.Name])
		}
		wantMode := InventoryDirect
		if descriptor.Name == "deb" || descriptor.Name == "rpm" {
			wantMode = InventoryBuildSubset
		}
		if descriptor.InventoryMode != wantMode {
			t.Errorf("profile %s inventory mode = %q, want %q", descriptor.Name, descriptor.InventoryMode, wantMode)
		}
		wantUnused := []string(nil)
		if descriptor.Name == "deci" {
			wantUnused = []string{"node-8", "node-9"}
		}
		if !reflect.DeepEqual(descriptor.InventoryUnusedNodes, wantUnused) {
			t.Errorf("profile %s unused inventory nodes = %v, want %v", descriptor.Name, descriptor.InventoryUnusedNodes, wantUnused)
		}
	}
	if !reflect.DeepEqual(got, expectedNames) {
		t.Fatalf("catalog names = %v, want %v", got, expectedNames)
	}

	embedded, err := fs.Glob(profileassets.FS, "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(embedded)
	wantFiles := make([]string, 0, len(expectedNames))
	for _, name := range expectedNames {
		wantFiles = append(wantFiles, name+".yaml")
	}
	if !reflect.DeepEqual(embedded, wantFiles) {
		t.Fatalf("embedded YAML = %v, want %v", embedded, wantFiles)
	}
	for _, file := range embedded {
		if strings.Contains(file, "example") {
			t.Errorf("example profile embedded: %s", file)
		}
	}
}

func TestEmbeddedProfilesAreTheOwnedContract(t *testing.T) {
	t.Parallel()
	if len(expectedProfileNodeCounts) != len(expectedNames) || len(expectedProfileSHA256) != len(expectedNames) || len(expectedResolvedSHA256) != len(expectedNames) || len(expectedProfileInventoryRefs) != len(expectedNames) {
		t.Fatal("profile contract maps must contain exactly the catalog names")
	}
	totalNodes := 0
	for _, name := range expectedNames {
		file, _, err := Load(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		yamlData, _, err := YAML(name)
		if err != nil {
			t.Fatalf("read YAML %s: %v", name, err)
		}
		digest := sha256.Sum256(yamlData)
		if got, want := hex.EncodeToString(digest[:]), expectedProfileSHA256[name]; got != want {
			t.Errorf("%s YAML sha256 = %s, want owned contract %s", name, got, want)
		}
		resolved, err := file.Resolve()
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		resolvedHash, err := spec.Hash(resolved)
		if err != nil {
			t.Fatalf("hash resolved %s: %v", name, err)
		}
		if want := expectedResolvedSHA256[name]; resolvedHash != want {
			t.Errorf("%s resolved hash = %s, want owned contract %s", name, resolvedHash, want)
		}
		wantNodes, ok := expectedProfileNodeCounts[name]
		if !ok {
			t.Fatalf("%s has no node-count contract", name)
		}
		if len(file.Nodes) != wantNodes {
			t.Errorf("%s node count = %d, want %d", name, len(file.Nodes), wantNodes)
		}
		totalNodes += len(file.Nodes)
		if file.SSH.User != "dba" {
			t.Errorf("%s login user = %q, want dba", name, file.SSH.User)
		}
		if file.Arch != "native" {
			t.Errorf("%s architecture = %q, want native", name, file.Arch)
		}
		if file.Network != (config.NetworkConfig{Mode: "private", CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8"}) {
			t.Errorf("%s network contract = %#v", name, file.Network)
		}
		for index, node := range file.Nodes {
			if node.Control != (index == 0) {
				t.Errorf("%s node %s control = %v, want %v", name, node.Name, node.Control, index == 0)
			}
			if strings.HasPrefix(node.Name, "minio") {
				assertMinIODataDisks(t, node)
			} else {
				assertOrdinaryNodeDisk(t, node)
			}
			wantAliases := []string(nil)
			if index == 0 {
				wantAliases = []string{"i.pigsty", "api.pigsty", "cli.pigsty", "sss.pigsty", "adm.pigsty", "lab.pigsty", "wiki.pigsty", "git.pigsty"}
				if node.Name != "meta" {
					wantAliases = append([]string{"meta"}, wantAliases...)
				}
			}
			if !reflect.DeepEqual(node.HostAliases, wantAliases) {
				t.Errorf("%s node %s aliases = %v, want %v", name, node.Name, node.HostAliases, wantAliases)
			}
			if len(node.Forwards) != 0 {
				t.Errorf("%s node %s contains profile forwards", name, node.Name)
			}
			for _, disk := range node.Disks {
				if disk.Filesystem != "auto" || disk.Persistent {
					t.Errorf("%s node %s disk %s filesystem/persistent = %q/%v, want auto/false", name, node.Name, disk.Name, disk.Filesystem, disk.Persistent)
				}
			}
		}
	}
	if totalNodes != 85 {
		t.Errorf("owned profile node total = %d, want 85", totalNodes)
	}
}

func TestEveryCatalogProfileIsStrictReadableAndNamed(t *testing.T) {
	t.Parallel()
	for _, name := range expectedNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file, descriptor, err := Load(name)
			if err != nil {
				t.Fatal(err)
			}
			if file.Name != name || descriptor.Name != name {
				t.Fatalf("loaded file=%q descriptor=%q, want %q", file.Name, descriptor.Name, name)
			}
			if file.SSH.User != "dba" {
				t.Fatalf("embedded profile %s login user = %q, want dba", name, file.SSH.User)
			}
			if err := file.Validate(); err != nil {
				t.Fatalf("validate loaded profile: %v", err)
			}
			yamlData, yamlDescriptor, err := YAML(name)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := config.Decode(bytes.NewReader(yamlData))
			if err != nil {
				t.Fatalf("strict decode exported YAML: %v", err)
			}
			if decoded.Name != name || yamlDescriptor.Name != name {
				t.Fatalf("exported profile names = %q/%q", decoded.Name, yamlDescriptor.Name)
			}
		})
	}
	if _, _, err := Load("example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(example) error = %v, want ErrNotFound", err)
	}
}

func TestRepresentativeEffectiveProfileSpecifications(t *testing.T) {
	t.Parallel()

	full, _, err := Load("full")
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Nodes) != 4 || full.Nodes[0].Name != "meta" || full.Nodes[0].CPUs != 2 || int64(full.Nodes[0].Memory) != 4*spec.GiB {
		t.Fatalf("full topology = %#v", full.Nodes)
	}
	assertOrdinaryDataDisks(t, full)

	minio, _, err := Load("minio")
	if err != nil {
		t.Fatal(err)
	}
	if len(minio.Nodes) != 4 {
		t.Fatalf("minio node count = %d, want 4", len(minio.Nodes))
	}
	for _, node := range minio.Nodes {
		assertMinIODataDisks(t, node)
	}

	simu, _, err := Load("simu")
	if err != nil {
		t.Fatal(err)
	}
	if len(simu.Nodes) != 20 {
		t.Fatalf("simu node count = %d, want 20", len(simu.Nodes))
	}
	minioNodes := 0
	for _, node := range simu.Nodes {
		if strings.HasPrefix(node.Name, "minio") {
			minioNodes++
			assertMinIODataDisks(t, node)
			continue
		}
		assertOrdinaryNodeDisk(t, node)
	}
	if minioNodes != 4 {
		t.Fatalf("simu minio node count = %d, want 4", minioNodes)
	}
}

func assertOrdinaryDataDisks(t *testing.T, file config.File) {
	t.Helper()
	for _, node := range file.Nodes {
		assertOrdinaryNodeDisk(t, node)
	}
}

func assertOrdinaryNodeDisk(t *testing.T, node config.NodeConfig) {
	t.Helper()
	if len(node.Disks) != 1 || node.Disks[0].Name != "data" || node.Disks[0].Mount != "/data" || int64(node.Disks[0].Size) != 128*spec.GiB {
		t.Errorf("ordinary node %s disks = %#v", node.Name, node.Disks)
	}
}

func assertMinIODataDisks(t *testing.T, node config.NodeConfig) {
	t.Helper()
	if len(node.Disks) != 4 {
		t.Errorf("minio node %s disk count = %d, want 4", node.Name, len(node.Disks))
		return
	}
	for index, disk := range node.Disks {
		wantSuffix := string(rune('1' + index))
		if disk.Name != "data"+wantSuffix || disk.Mount != "/data"+wantSuffix || int64(disk.Size) != 32*spec.GiB {
			t.Errorf("minio node %s disk %d = %#v", node.Name, index, disk)
		}
	}
}

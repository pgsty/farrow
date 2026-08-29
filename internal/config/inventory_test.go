package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/spec"
)

func writeTestFile(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

const fullInventory = `---
all:
  vars:
    version: v3.6.0
    admin_ip: 10.10.10.10
    region: default
    vm_image: u24
    node_tune: tiny
  children:
    infra:
      hosts:
        10.10.10.10:
          infra_seq: 1
          nodename: meta
          vm_cpu: 4
          vm_mem: 8192
          vm_alias: [i.pigsty, a.pigsty]
    etcd:
      hosts:
        10.10.10.10: { etcd_seq: 1 }
      vars: { etcd_cluster: etcd }
    pg-meta:
      hosts:
        10.10.10.10: { pg_seq: 1, pg_role: primary }
      vars:
        pg_cluster: pg-meta
        pg_conf: crit.yml
    pg-test:
      hosts:
        10.10.10.11: { pg_seq: 1, pg_role: primary }
        10.10.10.12: { pg_seq: 2, pg_role: replica, vm_mem: "4GiB" }
        10.10.10.13: { pg_seq: 3 }
      vars:
        pg_cluster: pg-test
        vm_disks: [{ path: /data, size: 64 }]
`

func mustParseInventory(t *testing.T, text string) File {
	t.Helper()
	file, err := ParseInventory([]byte(text))
	if err != nil {
		t.Fatalf("parse inventory: %v", err)
	}
	return file
}

func TestParseInventoryFullLab(t *testing.T) {
	file := mustParseInventory(t, fullInventory)
	if file.Network.Mode != "private" || file.Network.CIDR != "10.10.10.0/24" {
		t.Fatalf("derived network is wrong: %+v", file.Network)
	}
	if file.SSH.User != "dba" {
		t.Fatalf("default ssh user, got %q", file.SSH.User)
	}
	if len(file.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(file.Nodes))
	}

	meta := file.Nodes[0]
	if meta.Name != "meta" || !meta.Control || meta.Address != "10.10.10.10" {
		t.Fatalf("meta identity/control: %+v", meta)
	}
	if meta.CPUs != 4 || int64(meta.Memory) != 8192<<20 || int64(meta.RootDisk) != 64*spec.GiB {
		t.Fatalf("meta resources: %+v", meta)
	}
	if len(meta.HostAliases) != 2 || meta.HostAliases[0] != "i.pigsty" {
		t.Fatalf("meta aliases: %v", meta.HostAliases)
	}
	if len(meta.Disks) != 1 || meta.Disks[0].Mount != "/data" || int64(meta.Disks[0].Size) != 128*spec.GiB || meta.Disks[0].Filesystem != "xfs" {
		t.Fatalf("meta default data disk: %+v", meta.Disks)
	}

	nodeOne := file.Nodes[1]
	if nodeOne.Name != "pg-test-1" || nodeOne.Control {
		t.Fatalf("pg-derived name: %+v", nodeOne)
	}
	if nodeOne.CPUs != 2 || int64(nodeOne.Memory) != 4096<<20 {
		t.Fatalf("node-1 defaults: %+v", nodeOne)
	}
	if len(nodeOne.Disks) != 1 || int64(nodeOne.Disks[0].Size) != 64*spec.GiB {
		t.Fatalf("group-level vm_disks: %+v", nodeOne.Disks)
	}

	if int64(file.Nodes[2].Memory) != 4*spec.GiB {
		t.Fatalf("string-size vm_mem: %+v", file.Nodes[2])
	}
	if file.Nodes[3].Name != "pg-test-3" {
		t.Fatalf("pg-derived fallback for bare host: %+v", file.Nodes[3])
	}
}

func TestParseInventoryBareHostGetsFullDefaults(t *testing.T) {
	file := mustParseInventory(t, `
all:
  children:
    nodes:
      hosts:
        10.10.10.13: {}
`)
	node := file.Nodes[0]
	if node.Name != "node-13" || node.CPUs != 2 || int64(node.Memory) != 4096<<20 || int64(node.RootDisk) != 64*spec.GiB {
		t.Fatalf("bare host defaults: %+v", node)
	}
	if len(node.Disks) != 1 || node.Disks[0].Mount != "/data" || node.Disks[0].Filesystem != "xfs" {
		t.Fatalf("bare host default disk: %+v", node.Disks)
	}
	if node.Image != "d13" || !node.Control {
		t.Fatalf("bare host image/control: %+v", node)
	}
}

func TestParseInventoryDeploymentArchitecture(t *testing.T) {
	file := mustParseInventory(t, `
all:
  vars: { vm_arch: amd64 }
  children:
    nodes:
      hosts:
        10.10.10.10: {}
        10.10.10.11: {}
`)
	resolved, err := file.Resolve()
	if err != nil || file.Arch != "amd64" || resolved.Arch != "amd64" {
		t.Fatalf("resolved architecture = file:%q resolved:%q err:%v", file.Arch, resolved.Arch, err)
	}
	for name, text := range map[string]string{
		"empty": `
all:
  vars: { vm_arch: "" }
  children:
    nodes:
      hosts:
        10.10.10.10: {}
`,
		"unsupported": `
all:
  vars: { vm_arch: s390x }
  children:
    nodes:
      hosts:
        10.10.10.10: {}
`,
		"partial": `
all:
  children:
    nodes:
      hosts:
        10.10.10.10: { vm_arch: amd64 }
        10.10.10.11: {}
`,
		"mixed": `
all:
  children:
    nodes:
      hosts:
        10.10.10.10: { vm_arch: amd64 }
        10.10.10.11: { vm_arch: arm64 }
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseInventory([]byte(text)); err == nil || !strings.Contains(err.Error(), "vm_arch") {
				t.Fatalf("invalid vm_arch inventory error = %v", err)
			}
		})
	}
}

func TestParseInventoryExplicitEmptyDisksOverrides(t *testing.T) {
	file := mustParseInventory(t, `
all:
  children:
    nodes:
      hosts:
        10.10.10.10: { vm_disks: [] }
`)
	if len(file.Nodes[0].Disks) != 0 {
		t.Fatalf("explicit empty vm_disks must remove the default disk: %+v", file.Nodes[0].Disks)
	}
}

func TestParseInventorySiblingGroupConflictFails(t *testing.T) {
	_, err := ParseInventory([]byte(`
all:
  children:
    one:
      hosts: { 10.10.10.10: {} }
      vars: { vm_cpu: 2 }
    two:
      hosts: { 10.10.10.10: {} }
      vars: { vm_cpu: 4 }
`))
	if err == nil || !strings.Contains(err.Error(), "conflicting values") {
		t.Fatalf("expected sibling conflict error, got %v", err)
	}
}

func TestParseInventoryDeeperGroupWins(t *testing.T) {
	file := mustParseInventory(t, `
all:
  vars: { vm_cpu: 1 }
  children:
    parent:
      vars: { vm_cpu: 2 }
      children:
        child:
          hosts: { 10.10.10.10: {} }
          vars: { vm_cpu: 8 }
`)
	if file.Nodes[0].CPUs != 8 {
		t.Fatalf("deeper group must win: %+v", file.Nodes[0])
	}
}

func TestParseInventorySkipAndForeignHosts(t *testing.T) {
	file := mustParseInventory(t, `
all:
  children:
    nodes:
      hosts:
        10.10.10.10: {}
        10.10.10.20: { vm_skip: true }
`)
	if len(file.Nodes) != 1 || file.Nodes[0].Address != "10.10.10.10" {
		t.Fatalf("vm_skip host must be excluded: %+v", file.Nodes)
	}
}

func TestParseInventoryUnknownVMKeyFails(t *testing.T) {
	_, err := ParseInventory([]byte(`
all:
  children:
    nodes:
      hosts:
        10.10.10.10: { vm_cpus: 4 }
`))
	if err == nil || !strings.Contains(err.Error(), "unknown farrow variable") {
		t.Fatalf("expected strict vm_* namespace error, got %v", err)
	}
}

func TestParseInventoryTemplateValueFails(t *testing.T) {
	_, err := ParseInventory([]byte(`
all:
  children:
    nodes:
      hosts:
        10.10.10.10: { vm_image: "{{ default_image }}" }
`))
	if err == nil || !strings.Contains(err.Error(), "template expression") {
		t.Fatalf("expected literal-value error, got %v", err)
	}
}

func TestParseInventorySplitSubnetFails(t *testing.T) {
	_, err := ParseInventory([]byte(`
all:
  children:
    nodes:
      hosts:
        10.10.10.10: {}
        10.10.20.10: {}
`))
	if err == nil || !strings.Contains(err.Error(), "share one /24") {
		t.Fatalf("expected split-subnet error, got %v", err)
	}
}

func TestParseInventoryAdminUserContract(t *testing.T) {
	if _, err := ParseInventory([]byte(`
all:
  vars: { node_admin_username: root }
  children:
    nodes:
      hosts: { 10.10.10.10: {} }
`)); err != nil {
		t.Fatalf("custom admin user must parse: %v", err)
	}
	_, err := ParseInventory([]byte(`
all:
  vars: { node_admin_uid: 1000 }
  children:
    nodes:
      hosts: { 10.10.10.10: {} }
`))
	if err == nil || !strings.Contains(err.Error(), "fixed UID") {
		t.Fatalf("expected fixed-UID error, got %v", err)
	}
}

func TestParseInventoryPigstyVarsAreOpaque(t *testing.T) {
	file := mustParseInventory(t, `
all:
  vars:
    pg_version: 18
    repo_modules: infra,node,pgsql
    node_packages: [openssh-server]
  children:
    pg-meta:
      hosts: { 10.10.10.10: { pg_seq: 1, pg_role: primary } }
      vars:
        pg_cluster: pg-meta
        pg_users: [{ name: dbuser_meta, password: DBUser.Meta }]
`)
	if file.Nodes[0].Name != "pg-meta-1" {
		t.Fatalf("expected opaque handling plus pg naming, got %+v", file.Nodes[0])
	}
}

func TestDetectFormat(t *testing.T) {
	if format, err := DetectFormat([]byte(fullInventory)); err != nil || format != "inventory" {
		t.Fatalf("inventory detection: %q %v", format, err)
	}
	legacy := []byte("version: 1\nname: full\nnetwork: {mode: private}\nnodes: [{name: meta}]\n")
	if format, err := DetectFormat(legacy); err != nil || format != "legacy" {
		t.Fatalf("legacy detection: %q %v", format, err)
	}
	if _, err := DetectFormat([]byte("just: text\n")); err == nil {
		t.Fatalf("expected unrecognized-format error")
	}
}

func TestDiscoverLegacyConfigFails(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, directory, "farrow.yaml", "version: 1\nname: x\nnetwork: {mode: user}\nnodes: [{name: meta}]\n")
	_, _, err := Discover(directory, "")
	if !errors.Is(err, ErrLegacyConfig) {
		t.Fatalf("expected ErrLegacyConfig, got %v", err)
	}
}

func TestDiscoverPigstyYML(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, directory, "pigsty.yml", fullInventory)
	file, path, err := Discover(directory, "")
	if err != nil || !strings.HasSuffix(path, "pigsty.yml") || len(file.Nodes) != 4 {
		t.Fatalf("pigsty.yml discovery: path=%q nodes=%d err=%v", path, len(file.Nodes), err)
	}
}

func TestDiscoverPrefersFarrowYMLOverPigstyYML(t *testing.T) {
	directory := t.TempDir()
	writeTestFile(t, directory, "pigsty.yml", fullInventory)
	writeTestFile(t, directory, "farrow.yml", "all:\n  vars: {admin_ip: 10.10.10.10}\n  children:\n    nodes:\n      hosts:\n        10.10.10.10: {nodename: meta}\n")
	file, path, err := Discover(directory, "")
	if err != nil || !strings.HasSuffix(path, "farrow.yml") || len(file.Nodes) != 1 {
		t.Fatalf("farrow.yml priority: path=%q nodes=%d err=%v", path, len(file.Nodes), err)
	}
}

func TestDiscoverMissingConfig(t *testing.T) {
	_, _, err := Discover(t.TempDir(), "")
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("expected ErrNoConfig, got %v", err)
	}
}

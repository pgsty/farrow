package config

import (
	"strings"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/spec"
)

func TestStrictYAMLResolve(t *testing.T) {
	t.Parallel()
	input := `version: 1
name: full
network:
  mode: private
defaults:
  image: u24
  cpus: 1
  memory: 2GiB
  root_disk: 64GiB
ssh:
  user: dba
nodes:
  - name: meta
    control: true
    address: 10.10.10.10
    cpus: 2
    memory: 4GiB
    disks:
      - name: data
        size: 128GiB
        mount: /data
  - name: node-1
    address: 10.10.10.11
`
	file, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := file.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Network != "private" || resolved.Private.DHCPEnd != "10.10.10.8" || len(resolved.Nodes) != 2 || resolved.Nodes[0].Memory != 4*spec.GiB || resolved.Nodes[0].Disks[0].Size != 128*spec.GiB {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestStrictYAMLRejectsUnknownAndMultipleDocuments(t *testing.T) {
	t.Parallel()
	base := "version: 1\nname: quick\nnetwork: {mode: user}\nnodes: [{name: meta}]\n"
	if _, err := Decode(strings.NewReader(base + "unknown: true\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Decode(strings.NewReader(base + "---\n" + base)); err == nil {
		t.Fatal("multiple documents accepted")
	}
}

func TestPrivateAddressAndUserNodeValidation(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"version: 1\nname: bad\nnetwork: {mode: private}\nnodes: [{name: meta, address: 10.10.10.8}]\n",
		"version: 1\nname: bad\nnetwork: {mode: user}\nnodes: [{name: a}, {name: b}]\n",
	} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Errorf("invalid config accepted:\n%s", input)
		}
	}
}

func TestCustomPrivateNetworkAndRebase(t *testing.T) {
	t.Parallel()
	input := `version: 1
name: custom
network:
  mode: private
  cidr: 172.31.251.0/24
nodes:
  - {name: meta, address: 172.31.251.10}
  - {name: node-1, address: 172.31.251.11}
`
	file, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := file.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Private.HostAddress != "172.31.251.1" || resolved.Private.DHCPEnd != "172.31.251.8" {
		t.Fatalf("custom private = %#v", resolved.Private)
	}
	rebased, err := RebasePrivateNetwork(resolved, "10.77.88.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if rebased.Nodes[0].Address != "10.77.88.10" || rebased.Nodes[1].Address != "10.77.88.11" || rebased.Private.HostAddress != "10.77.88.1" {
		t.Fatalf("rebased = %#v", rebased)
	}
}

func TestCustomPrivateNetworkRequiresCoordinatedLayout(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"version: 1\nname: bad\nnetwork: {mode: private, cidr: 8.8.8.0/24}\nnodes: [{name: meta, address: 8.8.8.10}]\n",
		"version: 1\nname: bad\nnetwork: {mode: private, cidr: 172.31.251.0/24, host_address: 172.31.251.2}\nnodes: [{name: meta, address: 172.31.251.10}]\n",
		"version: 1\nname: bad\nnetwork: {mode: private, cidr: 172.31.251.0/24}\nnodes: [{name: meta, address: 10.10.10.10}]\n",
	} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted unsafe custom layout:\n%s", input)
		}
	}
}

func TestDiskMountTraversalAndSystemPathsAreRejected(t *testing.T) {
	t.Parallel()
	for _, mount := range []string{"/data/../../etc", "/data/..", "/data//nested", "/proc/data", "/usr/local/data", "/var/lib/piglet/data"} {
		input := "version: 1\nname: bad\nnetwork: {mode: user}\nnodes:\n  - name: meta\n    disks:\n      - {name: data, size: 1GiB, mount: " + mount + "}\n"
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Errorf("unsafe disk mount %q was accepted", mount)
		}
	}
}

func TestResolvedYAMLRoundTrip(t *testing.T) {
	t.Parallel()
	resolved := spec.Quick(true, true)
	file, err := FromResolved(resolved)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("decode generated YAML: %v\n%s", err, data)
	}
	again, err := decoded.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	hash1, _ := spec.Hash(resolved)
	hash2, _ := spec.Hash(again)
	if hash1 != hash2 {
		t.Fatalf("resolved hash changed:\n%s\n%s", hash1, hash2)
	}
}

func TestStorageRootAndSSHWaitTimeoutResolveAndRoundTrip(t *testing.T) {
	t.Parallel()
	input := `version: 1
name: quick
network: {mode: user}
ssh:
  user: operator
  wait_timeout: 750ms
storage:
  data_root: /var/tmp/piglet-test-data
nodes:
  - name: meta
`
	file, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := file.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SSHUser != "operator" || resolved.SSHWaitTimeoutNS != int64(750*time.Millisecond) || resolved.DataRoot != "/var/tmp/piglet-test-data" {
		t.Fatalf("resolved operational fields = %#v", resolved)
	}
	roundTrip, err := FromResolved(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.SSH.User != "operator" || time.Duration(roundTrip.SSH.WaitTimeout) != 750*time.Millisecond || roundTrip.Storage.DataRoot != "/var/tmp/piglet-test-data" {
		t.Fatalf("round-trip config = %#v", roundTrip)
	}
}

func TestStorageRootRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"relative", "/", "/tmp/../tmp/piglet"} {
		input := "version: 1\nname: quick\nnetwork: {mode: user}\nstorage: {data_root: " + root + "}\nnodes: [{name: meta}]\n"
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Errorf("unsafe storage.data_root %q accepted", root)
		}
	}
}

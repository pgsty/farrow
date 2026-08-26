package profile

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/config"
)

func TestScaleChangesOnlyCPUAndMemory(t *testing.T) {
	t.Parallel()
	source, descriptor, err := Load("full")
	if err != nil {
		t.Fatal(err)
	}
	before := cloneFile(source)
	scaled, err := ApplyOverrides(source, descriptor, Overrides{Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, before) {
		t.Fatal("ApplyOverrides mutated its source configuration")
	}
	if len(scaled.Nodes) != len(before.Nodes) {
		t.Fatalf("scaled node count = %d, want %d", len(scaled.Nodes), len(before.Nodes))
	}
	for index := range before.Nodes {
		oldNode, newNode := before.Nodes[index], scaled.Nodes[index]
		if newNode.CPUs != oldNode.CPUs*2 || newNode.Memory != oldNode.Memory*2 {
			t.Errorf("node %s resources = %d/%d, want %d/%d", oldNode.Name, newNode.CPUs, newNode.Memory, oldNode.CPUs*2, oldNode.Memory*2)
		}
		oldNode.CPUs, newNode.CPUs = 0, 0
		oldNode.Memory, newNode.Memory = 0, 0
		if !reflect.DeepEqual(oldNode, newNode) {
			t.Errorf("scale changed non-CPU/memory fields for node %s:\nold=%#v\nnew=%#v", oldNode.Name, oldNode, newNode)
		}
	}
	oldDefaults, newDefaults := before.Defaults, scaled.Defaults
	if newDefaults.CPUs != oldDefaults.CPUs*2 || newDefaults.Memory != oldDefaults.Memory*2 {
		t.Errorf("scaled defaults = %#v, source = %#v", newDefaults, oldDefaults)
	}
	oldDefaults.CPUs, newDefaults.CPUs = 0, 0
	oldDefaults.Memory, newDefaults.Memory = 0, 0
	if oldDefaults != newDefaults {
		t.Errorf("scale changed non-CPU/memory defaults: old=%#v new=%#v", oldDefaults, newDefaults)
	}
}

func TestScaleBoundsPoliciesAndOverflow(t *testing.T) {
	t.Parallel()
	full, fullDescriptor, err := Load("full")
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []int{-1, 0, 65} {
		if _, err := ApplyOverrides(full, fullDescriptor, Overrides{Scale: scale}); err == nil {
			t.Errorf("scale %d was accepted", scale)
		}
	}
	unchanged, err := ApplyOverrides(full, fullDescriptor, Overrides{Scale: DefaultScale})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged, full) {
		t.Fatal("scale 1 changed the profile")
	}

	for _, name := range []string{"deci", "simu"} {
		file, descriptor, err := Load(name)
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Scalable {
			t.Errorf("%s unexpectedly marked scalable", name)
		}
		if _, err := ApplyOverrides(file, descriptor, Overrides{Scale: 2}); err == nil {
			t.Errorf("non-scalable profile %s accepted scale 2", name)
		}
		if _, err := ApplyOverrides(file, descriptor, Overrides{Scale: DefaultScale}); err != nil {
			t.Errorf("non-scalable profile %s rejected scale 1: %v", name, err)
		}
	}

	overflow := cloneFile(full)
	overflow.Defaults.Memory = config.Size(math.MaxInt64/2 + 1)
	overflow.Nodes[0].Memory = config.Size(math.MaxInt64/2 + 1)
	if _, err := ApplyOverrides(overflow, fullDescriptor, Overrides{Scale: 2}); err == nil {
		t.Fatal("overflowing memory scale was accepted")
	}
	if _, err := multiplyInt(int(^uint(0)>>1), 2); err == nil {
		t.Fatal("overflowing CPU multiplication was accepted")
	}
}

func TestImageOverrideHonorsMixedPolicy(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"all", "deb", "oss", "pro", "rpm"} {
		file, descriptor, err := Load(name)
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.ImagePolicy != ImageMixed {
			t.Fatalf("%s image policy = %q", name, descriptor.ImagePolicy)
		}
		if _, err := ApplyOverrides(file, descriptor, Overrides{Scale: DefaultScale, Image: "el9"}); err == nil {
			t.Errorf("mixed profile %s accepted unforced image override", name)
		}
		uniform, err := ApplyOverrides(file, descriptor, Overrides{Scale: DefaultScale, Image: "el9", ForceUniformImage: true})
		if err != nil {
			t.Fatalf("force uniform image on %s: %v", name, err)
		}
		if uniform.Defaults.Image != "el9" {
			t.Errorf("%s default image = %q", name, uniform.Defaults.Image)
		}
		for _, node := range uniform.Nodes {
			if node.Image != "el9" {
				t.Errorf("%s node %s image = %q", name, node.Name, node.Image)
			}
		}
	}

	full, descriptor, err := Load("full")
	if err != nil {
		t.Fatal(err)
	}
	uniform, err := ApplyOverrides(full, descriptor, Overrides{Scale: DefaultScale, Image: "d13"})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range uniform.Nodes {
		if node.Image != "d13" {
			t.Errorf("homogeneous node %s image = %q", node.Name, node.Image)
		}
	}
	if _, err := ApplyOverrides(full, descriptor, Overrides{Scale: DefaultScale, ForceUniformImage: true}); err == nil {
		t.Fatal("force-uniform-image without image was accepted")
	}
}

func TestNetworkOverridePreservesNodeSuffixesAndEverythingElse(t *testing.T) {
	t.Parallel()
	source, descriptor, err := Load("full")
	if err != nil {
		t.Fatal(err)
	}
	before := cloneFile(source)
	custom, err := ApplyOverrides(source, descriptor, Overrides{Scale: DefaultScale, NetworkCIDR: "172.31.251.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, before) {
		t.Fatal("network override mutated its source")
	}
	if custom.Network.CIDR != "172.31.251.0/24" || custom.Network.HostAddress != "172.31.251.1" || custom.Network.DHCPEnd != "172.31.251.8" {
		t.Fatalf("network = %#v", custom.Network)
	}
	for index := range before.Nodes {
		oldNode, newNode := before.Nodes[index], custom.Nodes[index]
		if !strings.HasSuffix(newNode.Address, oldNode.Address[strings.LastIndex(oldNode.Address, "."):]) {
			t.Errorf("node %s address %s -> %s", oldNode.Name, oldNode.Address, newNode.Address)
		}
		oldNode.Address, newNode.Address = "", ""
		if !reflect.DeepEqual(oldNode, newNode) {
			t.Errorf("network override changed non-address fields for %s", oldNode.Name)
		}
	}
}

func TestEveryEmbeddedProfileSupportsOneCustomGlobalSubnet(t *testing.T) {
	t.Parallel()
	descriptors, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		source, _, err := LoadWithOverrides(descriptor.Name, Overrides{Scale: DefaultScale})
		if err != nil {
			t.Fatalf("load %s: %v", descriptor.Name, err)
		}
		custom, err := ApplyOverrides(source, descriptor, Overrides{Scale: DefaultScale, NetworkCIDR: "172.31.251.0/24"})
		if err != nil {
			t.Fatalf("custom %s: %v", descriptor.Name, err)
		}
		for index := range source.Nodes {
			oldSuffix := source.Nodes[index].Address[strings.LastIndex(source.Nodes[index].Address, "."):]
			if !strings.HasPrefix(custom.Nodes[index].Address, "172.31.251.") || !strings.HasSuffix(custom.Nodes[index].Address, oldSuffix) {
				t.Errorf("%s/%s address %s -> %s", descriptor.Name, source.Nodes[index].Name, source.Nodes[index].Address, custom.Nodes[index].Address)
			}
		}
	}
}

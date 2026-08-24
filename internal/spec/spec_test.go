package spec

import "testing"

func TestQuickDefaults(t *testing.T) {
	t.Parallel()
	got := Quick(true, true)
	if got.SSHUser != "dba" || got.Image != "u24" || got.Network != "user" {
		t.Fatalf("quick identity defaults = %#v", got)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("quick nodes = %d, want 1", len(got.Nodes))
	}
	node := got.Nodes[0]
	if node.Name != "meta" || node.CPUs != 2 || node.Memory != 4*GiB || node.RootDisk != 64*GiB {
		t.Fatalf("quick node defaults = %#v", node)
	}
	if len(node.Disks) != 1 || node.Disks[0].Size != 64*GiB || node.Disks[0].Mount != "/data" {
		t.Fatalf("quick data disk = %#v", node.Disks)
	}
	if len(node.Forwards) != 4 {
		t.Fatalf("quick forwards = %d, want 4", len(node.Forwards))
	}
}

func TestQuickOptionalDefaults(t *testing.T) {
	t.Parallel()
	got := Quick(false, false).Nodes[0]
	if len(got.Disks) != 0 || len(got.Forwards) != 0 {
		t.Fatalf("optional defaults were not disabled: %#v", got)
	}
}

func TestHashDeterministic(t *testing.T) {
	t.Parallel()
	first, err := Hash(Quick(true, true))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash(Quick(true, true))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("hashes %q and %q", first, second)
	}
}

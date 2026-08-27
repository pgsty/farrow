package lease

import (
	"context"
	"testing"

	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/qemu"
)

func testInvocation() qemu.Invocation {
	return qemu.Invocation{Binary: "/opt/qemu/bin/qemu-system-aarch64", Args: []string{"-name", "node-1"}}
}

func reshapeAddNode(t *testing.T, value Lease, name string, lastOctet byte) Lease {
	t.Helper()
	vmUUID, err := identity.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	grown := value
	grown.Nodes = append(append([]Node(nil), value.Nodes...), Node{
		Name: name, Address: nodeAddress(lastOctet),
		ManagementMAC: nodeMAC("11", lastOctet), PrivateMAC: nodeMAC("aa", lastOctet),
		VMUUID: vmUUID, Phase: Reserved,
	})
	return grown
}

func nodeAddress(lastOctet byte) string { return "10.10.10." + itoa(lastOctet) }

func nodeMAC(prefix string, lastOctet byte) string {
	return "02:" + prefix + ":22:33:44:" + hexByte(lastOctet)
}

func itoa(value byte) string {
	digits := "0123456789"
	if value < 10 {
		return string(digits[value])
	}
	if value < 100 {
		return string(digits[value/10]) + string(digits[value%10])
	}
	return string(digits[value/100]) + string(digits[(value/10)%10]) + string(digits[value%10])
}

func hexByte(value byte) string {
	const digits = "0123456789abcdef"
	return string(digits[value>>4]) + string(digits[value&0xf])
}

func TestReshapeGrowsAndShrinksOwnedLease(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	base := newLease(t, 10)
	acquired, err := store.Acquire(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}

	grown := reshapeAddNode(t, acquired.Lease, "node-1", 11)
	result, err := store.Reshape(context.Background(), grown)
	if err != nil || result.Action != "reshaped" || len(result.Lease.Nodes) != 2 {
		t.Fatalf("grow reshape = %#v, %v", result, err)
	}
	if result.Lease.Generation != acquired.Lease.Generation+1 {
		t.Fatalf("reshape must bump the generation: %#v", result.Lease)
	}

	// Surviving reservations may not change identity.
	mutated := reshapeAddNode(t, acquired.Lease, "node-2", 12)
	mutated.Nodes[0].Address = "10.10.10.99"
	if _, err := store.Reshape(context.Background(), mutated); err == nil {
		t.Fatal("reshape changed a surviving reservation without conflict")
	}

	// A reserved (non-stopped) node cannot be dropped.
	shrunk := acquired.Lease
	shrunk.Nodes = shrunk.Nodes[:1]
	if _, err := store.Reshape(context.Background(), shrunk); err == nil {
		t.Fatal("reshape dropped a non-stopped node without conflict")
	}

	// Mark node-1 stopped, then dropping it succeeds.
	current, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	for index := range current.Nodes {
		if current.Nodes[index].Name == "node-1" {
			current.Nodes[index].Phase = Stopped
			current.Nodes[index].Runtime = RuntimePaths{Directory: "/run/x", QMP: "/run/x/qmp.sock", PIDFile: "/run/x/qemu.pid"}
			current.Nodes[index].Invocation = testInvocation()
		}
	}
	if _, err := store.Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	final, err := store.Reshape(context.Background(), shrunk)
	if err != nil || len(final.Lease.Nodes) != 1 || final.Lease.Nodes[0].Name != "meta" {
		t.Fatalf("drop reshape = %#v, %v", final, err)
	}
}

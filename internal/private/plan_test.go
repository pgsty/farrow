package private

import (
	"reflect"
	"testing"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/spec"
)

func privateResolved() spec.Resolved {
	return spec.Resolved{
		Schema: 1, Name: "full", Image: "u24", Network: "private", SSHUser: "dba",
		Private: &spec.PrivateNetwork{CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8"},
		Nodes: []spec.Node{
			{Name: "meta", Control: true, Address: "10.10.10.10", CPUs: 2, Memory: 4 * spec.GiB, RootDisk: 64 * spec.GiB, Aliases: []string{"admin.example"}},
			{Name: "node-1", Address: "10.10.10.11", CPUs: 1, Memory: 2 * spec.GiB, RootDisk: 32 * spec.GiB},
		},
	}
}

func TestBuildPrivateIntentDeterministicAndShort(t *testing.T) {
	t.Parallel()
	projectID, _ := project.NewUUID()
	uuids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	index := 0
	plan, err := Build(privateResolved(), projectID, 501, nil, func() (string, error) {
		value := uuids[index]
		index++
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Control != "meta" || len(plan.Nodes) != 2 || len(plan.Lease.Nodes) != 2 {
		t.Fatalf("private plan = %#v", plan)
	}
	for _, node := range plan.Nodes {
		if len(node.Runtime.QMP) > 103 || node.ManagementMAC == node.PrivateMAC || node.Runtime.Directory == "" {
			t.Fatalf("node intent = %#v", node)
		}
	}
	if plan.Lease.Nodes[0].Name != "meta" || plan.Lease.Nodes[1].Name != "node-1" {
		t.Fatalf("lease nodes are not canonical: %#v", plan.Lease.Nodes)
	}
	second, err := Build(privateResolved(), projectID, 501, nil, func() (string, error) {
		index--
		return uuids[index], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Nodes[0].ManagementMAC != second.Nodes[0].ManagementMAC || plan.Nodes[0].PrivateMAC != second.Nodes[0].PrivateMAC || plan.Nodes[0].Runtime != second.Nodes[0].Runtime {
		t.Fatal("deterministic private identities changed")
	}
}

func TestBuildPrivateIntentReusesLeaseUUIDs(t *testing.T) {
	t.Parallel()
	projectID, _ := project.NewUUID()
	first, err := Build(privateResolved(), projectID, 501, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	existing := first.Lease
	existing.Schema = lease.Schema
	existing.Generation = 1
	existing.OwnerUID = 501
	second, err := Build(privateResolved(), projectID, 501, &existing, func() (string, error) {
		t.Fatal("UUID source called during lease reentry")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Lease.Nodes, second.Lease.Nodes) {
		t.Fatalf("reentered UUIDs changed: %#v %#v", first.Lease.Nodes, second.Lease.Nodes)
	}
}

func TestBuildPrivateIntentRejectsInvalidControlAndDuplicateAddress(t *testing.T) {
	t.Parallel()
	projectID, _ := project.NewUUID()
	resolved := privateResolved()
	resolved.Nodes[0].Control = false
	if _, err := Build(resolved, projectID, 501, nil, nil); err == nil {
		t.Fatal("private plan without control was accepted")
	}
	resolved = privateResolved()
	resolved.Nodes[1].Address = resolved.Nodes[0].Address
	if _, err := Build(resolved, projectID, 501, nil, nil); err == nil {
		t.Fatal("duplicate private address was accepted")
	}
}

func TestMaterializeExistingForwardPorts(t *testing.T) {
	t.Parallel()
	desired := privateResolved()
	desired.Nodes[0].Forwards = []spec.Forward{{Bind: "127.0.0.1", Host: 15432, Guest: 5432, Protocol: "tcp"}}
	persisted := cloneResolved(desired)
	persisted.Nodes[0].Forwards[0] = spec.WithMaterializedHost(persisted.Nodes[0].Forwards[0], 25432)

	got := materializeExistingForwardPorts(desired, persisted)
	if got.Nodes[0].Forwards[0].Host != 25432 {
		t.Fatalf("materialized host port = %d", got.Nodes[0].Forwards[0].Host)
	}
	if desired.Nodes[0].Forwards[0].Host != 15432 {
		t.Fatal("materialization mutated the desired input")
	}
	desired.Nodes[0].Forwards[0].Host = 16432
	if got := materializeExistingForwardPorts(desired, persisted); got.Nodes[0].Forwards[0].Host != 16432 {
		t.Fatal("changed host request reused a persisted materialized port")
	}
	desired.Nodes[0].Forwards[0].Host = 15432
	desired.Nodes[0].Forwards[0].Guest = 6432
	if got := materializeExistingForwardPorts(desired, persisted); got.Nodes[0].Forwards[0].Host != 15432 {
		t.Fatal("unrelated guest forward reused a persisted host port")
	}
}

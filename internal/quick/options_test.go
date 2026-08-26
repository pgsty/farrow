package quick

import (
	"errors"
	"testing"

	"github.com/pgsty/farrow/internal/spec"
)

func TestOptionsResolve(t *testing.T) {
	t.Parallel()
	resolved, err := (Options{Image: "el9", CPUs: 4, Memory: 8 * spec.GiB, RootDisk: 100 * spec.GiB, DataDisk: 128 * spec.GiB, NoDefaultForwards: true, Forwards: []spec.Forward{{Bind: "127.0.0.1", Host: 19000, Guest: 9000, Protocol: "tcp"}}}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	node := resolved.Nodes[0]
	if resolved.Image != "el9" || node.CPUs != 4 || node.Memory != 8*spec.GiB || node.RootDisk != 100*spec.GiB || node.Disks[0].Size != 128*spec.GiB || len(node.Forwards) != 1 {
		t.Fatalf("resolved options = %#v", resolved)
	}
	if _, err := (Options{NoDataDisk: true, DataDisk: 1}).Resolve(); err == nil {
		t.Fatal("conflicting data flags accepted")
	}
	if _, err := (Options{NoDefaultForwards: true, Forwards: []spec.Forward{
		{Bind: "127.0.0.1", Host: 19000, Guest: 9000, Protocol: "tcp"},
		{Bind: "127.0.0.1", Host: 19000, Guest: 9001, Protocol: "tcp"},
	}}).Resolve(); err == nil {
		t.Fatal("duplicate host listener accepted")
	}
}

func TestDriftClassificationAndPortReuse(t *testing.T) {
	t.Parallel()
	existing := spec.Quick(true, true)
	existing.Nodes[0].Forwards[0] = spec.WithMaterializedHost(existing.Nodes[0].Forwards[0], 25432)
	desired := materializeExistingPorts(spec.Quick(true, true), existing)
	if err := compareDesired(existing, desired); err != nil {
		t.Fatalf("materialized port should be reused: %v", err)
	}
	changedRequest := spec.Quick(true, true)
	changedRequest.Nodes[0].Forwards[0].Host = 16432
	changedRequest = materializeExistingPorts(changedRequest, existing)
	err := compareDesired(existing, changedRequest)
	var drift *DriftError
	if !errors.As(err, &drift) || drift.Action != "restart" || changedRequest.Nodes[0].Forwards[0].Host != 16432 {
		t.Fatalf("changed host request was hidden: desired=%#v drift=%#v err=%v", changedRequest.Nodes[0].Forwards[0], drift, err)
	}
	desired.Nodes[0].CPUs = 4
	err = compareDesired(existing, desired)
	if !errors.As(err, &drift) || drift.Action != "restart" {
		t.Fatalf("CPU drift = %#v, %v", drift, err)
	}
	desired = existing
	desired.Image = "el9"
	err = compareDesired(existing, desired)
	if !errors.As(err, &drift) || drift.Action != "recreate" {
		t.Fatalf("image drift = %#v, %v", drift, err)
	}
}

func TestLegacyRemappedForwardUsesMaterializedHostAsCompatibilityBaseline(t *testing.T) {
	t.Parallel()
	legacy := spec.Quick(true, true)
	legacy.Nodes[0].Forwards[0].Host = 25432
	unchangedOldRequest := materializeExistingPorts(spec.Quick(true, true), legacy)
	if err := compareDesired(legacy, unchangedOldRequest); err == nil {
		t.Fatal("legacy remap guessed an unavailable original request and hid drift")
	}
	explicitPersistedPort := spec.Quick(true, true)
	explicitPersistedPort.Nodes[0].Forwards[0].Host = 25432
	explicitPersistedPort = materializeExistingPorts(explicitPersistedPort, legacy)
	if err := compareDesired(legacy, explicitPersistedPort); err != nil {
		t.Fatalf("legacy materialized host was not accepted as compatibility baseline: %v", err)
	}
}

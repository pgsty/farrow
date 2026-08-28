package doctor

import (
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func TestReadableNetworkInstallationIncludesProtectedState(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]bool{
		"exact": true, "protected": true, "absent": false, "partial": false, "invalid": false,
	} {
		if got := readableNetworkInstallation(status); got != want {
			t.Errorf("status=%q got=%t want=%t", status, got, want)
		}
	}
}

func TestNetworkProbeAddressesExcludeAppliedDeployment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	layout := subnet.Default()
	resolved := spec.Resolved{
		Schema: 1, Name: "farrow", Image: "u24", Network: "private", SSHUser: "dba",
		Private: &spec.PrivateNetwork{CIDR: layout.CIDR(), HostAddress: layout.HostAddress(), DHCPEnd: layout.DHCPEnd()},
		Nodes: []spec.Node{
			{Name: "meta", Address: layout.Address(10)},
			{Name: "node-1", Address: layout.Address(11)},
		},
	}
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := (state.Store{Root: root}).WriteDeployment(state.DeploymentState{
		Schema: state.DeploymentSchema, FarrowVersion: "dev", SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	addresses := networkProbeAddresses(layout)
	seen := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		seen[address] = true
	}
	if seen[layout.Address(10)] || seen[layout.Address(11)] {
		t.Fatalf("applied deployment addresses remained eligible: %v", addresses)
	}
	if !seen[layout.Address(9)] || !seen[layout.Address(12)] || len(addresses) != len(layout.StaticAddresses())-2 {
		t.Fatalf("unexpected eligible addresses: len=%d first=%t next=%t", len(addresses), seen[layout.Address(9)], seen[layout.Address(12)])
	}
}

func TestNetworkProbeAddressesKeepAllWithoutMatchingDeployment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	layout := subnet.Default()
	if got, want := len(networkProbeAddresses(layout)), len(layout.StaticAddresses()); got != want {
		t.Fatalf("eligible addresses without deployment = %d, want %d", got, want)
	}
}

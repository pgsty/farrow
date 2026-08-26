package private

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/state"
)

func testLeaseRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "lease-root")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSynchronizePreparedStateIntoLeaseAndFinalize(t *testing.T) {
	projectValue, config, outcomes := commitFixture(t, &fakePrivateDisks{})
	committed, err := CommitPrepared(context.Background(), projectValue, config, outcomes, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	leaseRoot := testLeaseRoot(t)
	leaseStore := lease.Store{Root: leaseRoot, OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1}
	acquired, err := leaseStore.Acquire(context.Background(), config.Plan.Lease)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := SynchronizeLease(acquired.Lease, committed.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := leaseStore.Update(context.Background(), desired)
	if err != nil || updated.Lease.Generation != 2 {
		t.Fatalf("lease update = %#v, %v", updated, err)
	}
	for _, node := range updated.Lease.Nodes {
		if node.Phase != lease.Prepared || node.Runtime.QMP == "" || node.Invocation.Binary == "" {
			t.Fatalf("prepared lease node = %#v", node)
		}
	}
	for _, node := range committed.Nodes {
		if err := FinalizePrepared(projectValue, node.Node, LeaseNodeVerifier(updated.Lease, lease.Prepared)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSynchronizeLeaseRejectsUUIDAndPhaseMismatch(t *testing.T) {
	projectValue, config, outcomes := commitFixture(t, &fakePrivateDisks{})
	committed, err := CommitPrepared(context.Background(), projectValue, config, outcomes, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	badUUID := append([]state.NodeState(nil), committed.Nodes...)
	badUUID[0].VMUUID = "11111111-1111-4111-8111-111111111111"
	if _, err := SynchronizeLease(config.Plan.Lease, badUUID); err == nil {
		t.Fatal("mismatched VM UUID entered lease")
	}
	badPhase := append([]state.NodeState(nil), committed.Nodes...)
	badPhase[0].Phase = state.Destroying
	if _, err := SynchronizeLease(config.Plan.Lease, badPhase); err == nil {
		t.Fatal("destroying node phase entered lease")
	}
}

package lease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
)

func testStore(t *testing.T) Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "lease-root")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	return Store{Root: root, OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1, Now: func() time.Time { return time.Unix(100, 0).UTC() }}
}

func newLease(t *testing.T, lastOctet byte) Lease {
	t.Helper()
	projectID, err := project.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	vmUUID, err := project.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return Lease{
		ProjectID: projectID, CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8",
		Nodes: []Node{{
			Name: "meta", Address: fmt.Sprintf("10.10.10.%d", lastOctet),
			ManagementMAC: fmt.Sprintf("02:11:22:33:44:%02x", lastOctet), PrivateMAC: fmt.Sprintf("02:aa:bb:cc:dd:%02x", lastOctet),
			VMUUID: vmUUID, Phase: Reserved,
		}},
	}
}

func TestAcquireIdempotentAndConflictingReservation(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	desired := newLease(t, 10)
	first, err := store.Acquire(context.Background(), desired)
	if err != nil || first.Action != "acquired" || !first.Apply || first.Lease.Generation != 1 {
		t.Fatalf("first acquire = %#v, %v", first, err)
	}
	status, err := store.Inspect()
	if err != nil || !status.Available || !status.Active || status.Lease.ProjectID != desired.ProjectID {
		t.Fatalf("active lease status = %#v, %v", status, err)
	}
	second, err := store.Acquire(context.Background(), desired)
	if err != nil || second.Action != "reentered" || second.Apply || !reflect.DeepEqual(second.Lease, first.Lease) {
		t.Fatalf("idempotent acquire = %#v, %v", second, err)
	}
	changed := desired
	changed.Nodes = append([]Node(nil), desired.Nodes...)
	changed.Nodes[0].Address = "10.10.10.11"
	if _, err := store.Acquire(context.Background(), changed); err == nil {
		t.Fatal("same project changed reservation without conflict")
	} else {
		var conflict *ConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("changed reservation error = %T %v", err, err)
		}
	}
	other := newLease(t, 12)
	if _, err := store.Acquire(context.Background(), other); err == nil {
		t.Fatal("second project acquired active global lease")
	} else {
		var conflict *ConflictError
		if !errors.As(err, &conflict) || conflict.Existing.ProjectID != desired.ProjectID {
			t.Fatalf("project conflict = %#v, %v", conflict, err)
		}
	}
}

func TestAcquireSupportsOneCanonicalCustomGlobalSubnet(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	desired := newLease(t, 10)
	desired.CIDR = "172.30.50.0/24"
	desired.HostAddress = "172.30.50.1"
	desired.DHCPEnd = "172.30.50.8"
	desired.Nodes[0].Address = "172.30.50.10"
	if _, err := store.Acquire(context.Background(), desired); err != nil {
		t.Fatalf("custom global subnet rejected: %v", err)
	}
	invalid := desired
	invalid.ProjectID, _ = project.NewUUID()
	invalid.Nodes = append([]Node(nil), desired.Nodes...)
	invalid.Nodes[0].Address = "172.30.50.8"
	if err := Validate(Lease{Schema: Schema, Generation: 1, ProjectID: invalid.ProjectID, OwnerUID: os.Getuid(), CIDR: invalid.CIDR, HostAddress: invalid.HostAddress, DHCPEnd: invalid.DHCPEnd, Nodes: invalid.Nodes, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}); err == nil {
		t.Fatal("node inside DHCP pool was accepted")
	}
}

func TestConcurrentAcquireHasExactlyOneWinner(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	const contenders = 24
	var wait sync.WaitGroup
	results := make(chan error, contenders)
	for index := 0; index < contenders; index++ {
		desired := newLease(t, byte(10+index))
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.Acquire(context.Background(), desired)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			conflicts++
		} else {
			t.Errorf("unexpected acquire error: %v", err)
		}
	}
	if winners != 1 || conflicts != contenders-1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	if _, err := store.Read(); err != nil {
		t.Fatalf("winning lease is invalid: %v", err)
	}
}

func TestLeaseProcessHelper(t *testing.T) {
	if os.Getenv("FARROW_LEASE_PROCESS_HELPER") != "1" {
		return
	}
	root := os.Getenv("FARROW_LEASE_PROCESS_ROOT")
	projectID := os.Getenv("FARROW_LEASE_PROCESS_PROJECT")
	address := os.Getenv("FARROW_LEASE_PROCESS_ADDRESS")
	vmUUID := os.Getenv("FARROW_LEASE_PROCESS_VM_UUID")
	desired := Lease{
		ProjectID: projectID, CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8",
		Nodes: []Node{{Name: "meta", Address: address, ManagementMAC: "02:11:22:33:44:55", PrivateMAC: "02:aa:bb:cc:dd:ee", VMUUID: vmUUID, Phase: Reserved}},
	}
	store := Store{Root: root, OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1}
	if _, err := store.Acquire(context.Background(), desired); err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			os.Exit(6)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCrossProcessAcquireHasOneWinner(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	projects := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	addresses := []string{"10.10.10.10", "10.10.10.11"}
	uuids := []string{"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"}
	commands := make([]*exec.Cmd, 0, 2)
	for index := 0; index < 2; index++ {
		command := exec.Command(executable, "-test.run=^TestLeaseProcessHelper$")
		command.Env = append(os.Environ(),
			"FARROW_LEASE_PROCESS_HELPER=1",
			"FARROW_LEASE_PROCESS_ROOT="+store.Root,
			"FARROW_LEASE_PROCESS_PROJECT="+projects[index],
			"FARROW_LEASE_PROCESS_ADDRESS="+addresses[index],
			"FARROW_LEASE_PROCESS_VM_UUID="+uuids[index],
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	winners, conflicts := 0, 0
	for _, command := range commands {
		err := command.Wait()
		if err == nil {
			winners++
			continue
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 6 {
			conflicts++
		} else {
			t.Errorf("subprocess acquire failed unexpectedly: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("cross-process winners=%d conflicts=%d", winners, conflicts)
	}
}

func preparedNode(node Node) Node {
	runtimeDir := "/tmp/farrow-lease-meta"
	node.Phase = Prepared
	node.Runtime = RuntimePaths{Directory: runtimeDir, QMP: filepath.Join(runtimeDir, "qmp.sock"), PIDFile: filepath.Join(runtimeDir, "qemu.pid")}
	node.Invocation = qemu.Invocation{Binary: "/usr/bin/qemu-system-aarch64", Args: []string{"-name", node.Name}}
	return node
}

func TestUpdateReleaseAndLiveRuntimeRefusal(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	acquired, err := store.Acquire(context.Background(), newLease(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	desired := acquired.Lease
	desired.Nodes = append([]Node(nil), desired.Nodes...)
	desired.Nodes[0] = preparedNode(desired.Nodes[0])
	updated, err := store.Update(context.Background(), desired)
	if err != nil || updated.Lease.Generation != 2 || updated.Lease.Nodes[0].Phase != Prepared {
		t.Fatalf("prepared update = %#v, %v", updated, err)
	}
	running := updated.Lease
	running.Nodes = append([]Node(nil), running.Nodes...)
	running.Nodes[0].Phase = Running
	running.Nodes[0].Process = process.Identity{PID: 123, Executable: "/usr/bin/qemu-system-aarch64", Started: "start", ArgvHash: "hash"}
	runningResult, err := store.Update(context.Background(), running)
	if err != nil || runningResult.Lease.Generation != 3 {
		t.Fatalf("running update = %#v, %v", runningResult, err)
	}
	stopped := runningResult.Lease
	stopped.Nodes = append([]Node(nil), stopped.Nodes...)
	stopped.Nodes[0].Phase = Stopped
	stopped.Nodes[0].Process = process.Identity{}
	stoppedResult, err := store.Update(context.Background(), stopped)
	if err != nil || stoppedResult.Lease.Generation != 4 {
		t.Fatalf("stopped update = %#v, %v", stoppedResult, err)
	}
	liveAudit := func(_ context.Context, node Node) (Observation, error) {
		return Observation{Node: node.Name, Live: true, Authority: "qmp", Evidence: "matching name and UUID"}, nil
	}
	if _, err := store.Release(context.Background(), stopped.ProjectID, true, liveAudit); err == nil {
		t.Fatal("live lease was released")
	}
	deadAudit := func(_ context.Context, node Node) (Observation, error) {
		return Observation{Node: node.Name, Live: false, Authority: "dead", Evidence: "QMP absent and process identity dead"}, nil
	}
	dryRun, err := store.Release(context.Background(), stopped.ProjectID, false, deadAudit)
	if err != nil || dryRun.Apply || len(dryRun.Observations) != 1 {
		t.Fatalf("release dry run = %#v, %v", dryRun, err)
	}
	if _, err := store.Read(); err != nil {
		t.Fatalf("dry run removed lease: %v", err)
	}
	applied, err := store.Release(context.Background(), stopped.ProjectID, true, deadAudit)
	if err != nil || !applied.Apply {
		t.Fatalf("release apply = %#v, %v", applied, err)
	}
	if _, err := store.Read(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lease remains: %v", err)
	}
	absent, err := store.Release(context.Background(), stopped.ProjectID, true, deadAudit)
	if err != nil || absent.Action != "absent" || absent.Apply {
		t.Fatalf("idempotent absent release = %#v, %v", absent, err)
	}
}

func TestAbortOnlyRemovesDeadPreStartReservation(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	acquired, err := store.Acquire(context.Background(), newLease(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	deadAudit := func(_ context.Context, node Node) (Observation, error) {
		return Observation{Node: node.Name, Authority: "dead", Evidence: "no runtime"}, nil
	}
	if result, err := store.Abort(context.Background(), acquired.Lease.ProjectID, true, deadAudit); err != nil || result.Action != "abort" || !result.Apply {
		t.Fatalf("abort reservation = %#v, %v", result, err)
	}
	if _, err := store.Read(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aborted reservation remains: %v", err)
	}
	second, err := store.Acquire(context.Background(), newLease(t, 11))
	if err != nil {
		t.Fatal(err)
	}
	running := second.Lease
	running.Nodes = append([]Node(nil), running.Nodes...)
	running.Nodes[0] = preparedNode(running.Nodes[0])
	running.Nodes[0].Phase = Starting
	if _, err := store.Update(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Abort(context.Background(), running.ProjectID, true, deadAudit); err == nil {
		t.Fatal("starting lease was aborted")
	}
}

func TestGuardSerializesNetworkUninstallAgainstAcquire(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	release, err := store.Guard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blockedContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := store.Acquire(blockedContext, newLease(t, 10)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire was not blocked by uninstall guard: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(context.Background(), newLease(t, 11)); err != nil {
		t.Fatalf("acquire remained blocked after guard release: %v", err)
	}
}

func TestReclaimSameOwnerDeadLeaseAndRejectOtherUID(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	oldResult, err := store.Acquire(context.Background(), newLease(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	deadAudit := func(_ context.Context, node Node) (Observation, error) {
		return Observation{Node: node.Name, Authority: "dead", Evidence: "no QMP or process"}, nil
	}
	newDesired := newLease(t, 11)
	dryRun, err := store.Reclaim(context.Background(), newDesired, false, deadAudit)
	if err != nil || dryRun.Apply || dryRun.Previous.ProjectID != oldResult.Lease.ProjectID {
		t.Fatalf("reclaim dry run = %#v, %v", dryRun, err)
	}
	stillOld, _ := store.Read()
	if stillOld.ProjectID != oldResult.Lease.ProjectID {
		t.Fatal("dry-run reclaim changed lease")
	}
	applied, err := store.Reclaim(context.Background(), newDesired, true, deadAudit)
	if err != nil || !applied.Apply || applied.Lease.ProjectID != newDesired.ProjectID {
		t.Fatalf("reclaim apply = %#v, %v", applied, err)
	}
	otherUID := store
	otherUID.OwnerUID = os.Getuid() + 1
	if _, err := otherUID.Reclaim(context.Background(), newLease(t, 12), true, deadAudit); err == nil {
		t.Fatal("other UID reclaimed lease")
	} else {
		var conflict *ConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("other UID reclaim error = %T %v", err, err)
		}
	}
}

func TestReclaimHonorsRecentHeartbeatGrace(t *testing.T) {
	t.Parallel()
	current := time.Unix(100, 0).UTC()
	store := testStore(t)
	store.StaleAfter = 5 * time.Minute
	store.Now = func() time.Time { return current }
	if _, err := store.Acquire(context.Background(), newLease(t, 10)); err != nil {
		t.Fatal(err)
	}
	deadAudit := func(_ context.Context, node Node) (Observation, error) {
		return Observation{Node: node.Name, Authority: "dead", Evidence: "no runtime"}, nil
	}
	if _, err := store.Reclaim(context.Background(), newLease(t, 11), true, deadAudit); err == nil {
		t.Fatal("recent lease heartbeat was reclaimed")
	}
	current = current.Add(6 * time.Minute)
	if _, err := store.Reclaim(context.Background(), newLease(t, 11), true, deadAudit); err != nil {
		t.Fatalf("stale lease was not reclaimable after grace: %v", err)
	}
}

func TestLeaseSymlinkAndCorruptionRefusal(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	leasePath, _, _ := store.paths()
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, leasePath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(context.Background(), newLease(t, 10)); err == nil {
		t.Fatal("symlink lease was accepted")
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "preserve" {
		t.Fatalf("external lease target changed: %q %v", data, err)
	}
}

func TestLeaseRootModeBoundary(t *testing.T) {
	t.Parallel()
	store := testStore(t)
	if err := os.Chmod(store.Root, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(context.Background(), newLease(t, 10)); err == nil {
		t.Fatal("non-sticky lease root was accepted")
	}
}

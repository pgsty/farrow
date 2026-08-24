package private

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/piglet/internal/config"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/lease"
	netpreflight "github.com/pgsty/piglet/internal/network/preflight"
	"github.com/pgsty/piglet/internal/platform"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

func singlePrivateResolved() spec.Resolved {
	return spec.Resolved{
		Schema: 1, Name: "private", Image: "u24", Network: "private", SSHUser: "dba",
		Private: &spec.PrivateNetwork{CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8"},
		Nodes:   []spec.Node{{Name: "meta", Control: true, Address: "10.10.10.10", CPUs: 1, Memory: 2 * spec.GiB, RootDisk: 8 * spec.GiB, Forwards: []spec.Forward{{Bind: "127.0.0.1", Host: 15432, Guest: 5432, Protocol: "tcp"}}}},
	}
}

func TestMaterializePrivatePortsIsDeterministicAndNonColliding(t *testing.T) {
	t.Parallel()
	resolved := singlePrivateResolved()
	resolved.Nodes = append(resolved.Nodes, spec.Node{Name: "node-1", Address: "10.10.10.11", CPUs: 1, Memory: 2 * spec.GiB, RootDisk: 8 * spec.GiB})
	materialized, ports, err := materializePortsWithProbe(resolved, map[uint16]struct{}{2222: {}, 15432: {}}, func(string, uint16) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if ports["meta"] != 12222 || ports["node-1"] != 2223 || materialized.Nodes[0].Forwards[0].Host != 25432 {
		t.Fatalf("materialized ports=%v resolved=%#v", ports, materialized.Nodes[0].Forwards)
	}
	if resolved.Nodes[0].Forwards[0].Host != 15432 {
		t.Fatal("port materialization mutated caller-owned resolved spec")
	}
}

func TestPrivatePlanIsReadOnlyAndSupportsOneNode(t *testing.T) {
	work := t.TempDir()
	leaseRoot := testLeaseRoot(t)
	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		CWD: work, LeaseStore: &lease.Store{Root: leaseRoot, OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1},
		NativeProfile: func() (platform.Profile, error) { return profile, nil },
		DialSSHAddress: func(string, string) (net.Conn, error) {
			return nil, errors.New("fixture address unused")
		},
		HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
			return Backend{}, nil
		},
		NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
			return netpreflight.Report{Ready: true}
		},
	}
	plan, err := manager.Plan(context.Background(), singlePrivateResolved())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != "create" || len(plan.Nodes) != 1 || plan.Nodes[0] != "meta" {
		t.Fatalf("private plan = %#v", plan)
	}
	if _, err := os.Lstat(filepath.Join(work, ".piglet")); !os.IsNotExist(err) {
		t.Fatalf("read-only private plan created workspace state: %v", err)
	}
}

func TestPrivatePlanTreatsPreservedMarkerWithoutResolvedStateAsCreate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(work, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	profileValue, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		CWD: work, LeaseStore: &lease.Store{Root: testLeaseRoot(t), OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1},
		NativeProfile: func() (platform.Profile, error) { return profileValue, nil },
		DialSSHAddress: func(string, string) (net.Conn, error) {
			return nil, errors.New("fixture address unused")
		},
		HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
			return Backend{}, nil
		},
		NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
			return netpreflight.Report{Ready: true}
		},
	}
	plan, err := manager.Plan(context.Background(), singlePrivateResolved())
	if err != nil || plan.Action != "create" || plan.Destructive {
		t.Fatalf("preserved-marker plan=%#v err=%v", plan, err)
	}
}

func TestPrivateDriftPlansRecreateAndUpReturnsTypedConflict(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	persisted := singlePrivateResolved()
	persistedHash, err := spec.Hash(persisted)
	if err != nil {
		t.Fatal(err)
	}
	store := state.Store{Project: projectValue}
	if err := store.WriteProject(state.ProjectState{Schema: state.ProjectSchema, PigletVersion: "test", ProjectID: projectValue.Marker.ProjectID, SpecHash: persistedHash, Resolved: persisted, UpdatedAt: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	requested := cloneResolved(persisted)
	requested.Nodes[0].CPUs++
	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		CWD: work, LeaseStore: &lease.Store{Root: testLeaseRoot(t), OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1},
		NativeProfile: func() (platform.Profile, error) { return profile, nil },
		DialSSHAddress: func(string, string) (net.Conn, error) {
			return nil, errors.New("fixture address unused")
		},
		NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
			return netpreflight.Report{Ready: true}
		},
		HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
			return Backend{}, nil
		},
	}
	plan, err := manager.Plan(context.Background(), requested)
	if err != nil || plan.Action != "recreate" || !plan.Destructive {
		t.Fatalf("drift plan=%#v err=%v", plan, err)
	}
	if _, err := manager.Up(context.Background(), requested); !errors.Is(err, ErrRecreateRequired) {
		t.Fatalf("drift up error=%T %v, want ErrRecreateRequired", err, err)
	}
	if after, err := store.ReadProject(); err != nil || after.SpecHash != persistedHash {
		t.Fatalf("drift planning/up mutated project: %#v err=%v", after, err)
	}
}

func TestPrivatePlanStopsBeforeMutationOnNetworkMismatch(t *testing.T) {
	work := t.TempDir()
	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{
		CWD:           work,
		NativeProfile: func() (platform.Profile, error) { return profile, nil },
		NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
			return netpreflight.Report{Ready: false, ExitCode: 4, Findings: []netpreflight.Finding{{Code: "installation.network_mismatch", Severity: netpreflight.Error, Class: netpreflight.State, Evidence: "installed=10.10.10.0/24 requested=172.31.251.0/24"}}}
		},
		HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
			t.Fatal("host backend preflight ran after typed network mismatch")
			return Backend{}, nil
		},
	}
	requested, err := config.RebasePrivateNetwork(singlePrivateResolved(), "172.31.251.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Plan(context.Background(), requested); err == nil {
		t.Fatal("network mismatch was accepted")
	} else {
		var preflightErr *NetworkPreflightError
		if !errors.As(err, &preflightErr) || preflightErr.Report.ExitCode != 4 {
			t.Fatalf("error=%T %v", err, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(work, ".piglet")); !os.IsNotExist(err) {
		t.Fatalf("network mismatch created workspace state: %v", err)
	}
}

func TestRestartAndRecreatePreflightBeforeLifecycleMutation(t *testing.T) {
	for _, command := range []string{"restart", "recreate"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			work := filepath.Join(root, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				t.Fatal(err)
			}
			projectValue, err := project.Create(work, filepath.Join(root, "data"))
			if err != nil {
				t.Fatal(err)
			}
			resolved := singlePrivateResolved()
			hash, err := spec.Hash(resolved)
			if err != nil {
				t.Fatal(err)
			}
			store := state.Store{Project: projectValue}
			if err := store.WriteProject(state.ProjectState{Schema: state.ProjectSchema, PigletVersion: "test", ProjectID: projectValue.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: time.Unix(1, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			profile, err := platform.Resolve("darwin", "arm64")
			if err != nil {
				t.Fatal(err)
			}
			preflightCalls := 0
			manager := Manager{
				CWD:           work,
				LeaseStore:    &lease.Store{Root: testLeaseRoot(t), OwnerUID: os.Getuid(), ExpectedRootUID: os.Getuid(), StaleAfter: -1},
				NativeProfile: func() (platform.Profile, error) { return profile, nil },
				NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
					preflightCalls++
					return netpreflight.Report{Ready: false, ExitCode: 4, Findings: []netpreflight.Finding{{Code: "installation.network_mismatch", Severity: netpreflight.Error, Class: netpreflight.State, Evidence: "mismatch"}}}
				},
				HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
					t.Fatal("host preflight ran after the typed gate failed")
					return Backend{}, nil
				},
			}
			if command == "restart" {
				_, err = manager.Restart(context.Background())
			} else {
				_, err = manager.Recreate(context.Background())
			}
			var preflightErr *NetworkPreflightError
			if !errors.As(err, &preflightErr) || preflightCalls != 1 {
				t.Fatalf("command=%s calls=%d error=%T %v", command, preflightCalls, err, err)
			}
			if _, err := store.ReadProject(); err != nil {
				t.Fatalf("command=%s mutated/destroyed project state after failed preflight: %v", command, err)
			}
		})
	}
}

func TestBoundedConcurrency(t *testing.T) {
	t.Parallel()
	for nodes, want := range map[int]int{0: 1, 1: 1, 2: 2, 4: 4, 20: 4} {
		if got := boundedConcurrency(nodes); got != want {
			t.Errorf("boundedConcurrency(%d)=%d, want %d", nodes, got, want)
		}
	}
}

func TestManagerUsesResolvedReadinessTimeout(t *testing.T) {
	t.Parallel()
	resolved := singlePrivateResolved()
	resolved.SSHWaitTimeoutNS = int64(750 * time.Millisecond)
	timeout, err := (Manager{}).readyTimeout(resolved)
	if err != nil || timeout != 750*time.Millisecond {
		t.Fatalf("resolved timeout = %s, %v", timeout, err)
	}
	timeout, err = (Manager{ReadyTimeout: 2 * time.Second}).readyTimeout(resolved)
	if err != nil || timeout != 2*time.Second {
		t.Fatalf("manager override timeout = %s, %v", timeout, err)
	}
	resolved.SSHWaitTimeoutNS = -1
	if _, err := (Manager{}).readyTimeout(resolved); err == nil {
		t.Fatal("negative resolved timeout accepted")
	}
}

func TestPrivateMaterializesDataRootAndRejectsRootChange(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	desired := singlePrivateResolved()
	desired.DataRoot = filepath.Join(root, "configured")
	t.Setenv("PIGLET_DATA_HOME", filepath.Join(root, "environment"))
	materialized, err := (Manager{CWD: work}).materializeDataRoot(desired)
	if err != nil || materialized.DataRoot != filepath.Join(root, "environment") {
		t.Fatalf("materialized = %#v, %v", materialized, err)
	}

	t.Setenv("PIGLET_DATA_HOME", "")
	if _, err := project.Create(work, filepath.Join(root, "persisted")); err != nil {
		t.Fatal(err)
	}
	desired.DataRoot = filepath.Join(root, "different")
	if _, err := (Manager{CWD: work}).materializeDataRoot(desired); err == nil {
		t.Fatal("private data-root change was accepted without migration")
	}
}

func TestSelectedNodeNamesRejectUnknownAndDuplicate(t *testing.T) {
	t.Parallel()
	resolved := singlePrivateResolved()
	resolved.Nodes = append(resolved.Nodes, spec.Node{Name: "node-1", Address: "10.10.10.11", CPUs: 1, Memory: spec.GiB, RootDisk: 8 * spec.GiB})
	selected, err := selectedNodeNames(resolved, []string{"node-1"})
	if err != nil || len(selected) != 1 || selected[0] != "node-1" {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	if _, err := selectedNodeNames(resolved, []string{"missing"}); err == nil {
		t.Fatal("unknown private node selection was accepted")
	}
	if _, err := selectedNodeNames(resolved, []string{"meta", "meta"}); err == nil {
		t.Fatal("duplicate private node selection was accepted")
	}
}

func TestEnsureSSHAddressesUnusedDetectsConflict(t *testing.T) {
	t.Parallel()
	nodes := []spec.Node{{Name: "meta", Address: "10.10.10.10"}}
	if err := ensureSSHAddressesUnusedWithDial(nodes, func(_, address string) (net.Conn, error) {
		if address != "10.10.10.10:22" {
			t.Fatalf("probe address = %s", address)
		}
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	}); err == nil {
		t.Fatal("existing SSH listener was not reported as a conflict")
	}
	if err := ensureSSHAddressesUnusedWithDial(nodes, func(string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}); err != nil {
		t.Fatalf("unused address rejected: %v", err)
	}
}

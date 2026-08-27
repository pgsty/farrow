package private

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/config"
	"github.com/pgsty/farrow/internal/execx"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
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
	if materialized.Nodes[0].Forwards[0].RequestedHost != 15432 {
		t.Fatalf("materialized forward request evidence = %#v", materialized.Nodes[0].Forwards[0])
	}
	if resolved.Nodes[0].Forwards[0].Host != 15432 {
		t.Fatal("port materialization mutated caller-owned resolved spec")
	}
}

func planFixtureManager(t *testing.T) Manager {
	t.Helper()
	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	return Manager{
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
}

func TestPrivatePlanIsReadOnlyAndSupportsOneNode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("FARROW_HOME", root)
	manager := planFixtureManager(t)
	plan, err := manager.Plan(context.Background(), singlePrivateResolved())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != "create" || len(plan.Nodes) != 1 || plan.Nodes[0] != "meta" {
		t.Fatalf("private plan = %#v", plan)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("read-only private plan created the data root: %v", err)
	}
}

func TestPrivatePlanTreatsEmptyDataRootWithoutStateAsCreate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	manager := planFixtureManager(t)
	plan, err := manager.Plan(context.Background(), singlePrivateResolved())
	if err != nil || plan.Action != "create" || plan.Destructive {
		t.Fatalf("empty-root plan=%#v err=%v", plan, err)
	}
}

func TestPrivateDriftPlansRecreateAndUpReturnsTypedConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	persisted := singlePrivateResolved()
	persisted.DataRoot = root
	persistedHash, err := spec.Hash(persisted)
	if err != nil {
		t.Fatal(err)
	}
	store := state.Store{Root: root}
	if err := store.WriteDeployment(state.DeploymentState{Schema: state.DeploymentSchema, FarrowVersion: "test", SpecHash: persistedHash, Resolved: persisted, UpdatedAt: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	metaHash, err := spec.NodeHash(persisted, "meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNode(state.NodeState{
		Schema: state.NodeSchema, FarrowVersion: "test", Node: "meta",
		VMUUID: "018f4b8e-1234-4abc-9def-0123456789ab", Phase: state.Stopped, Generation: 1,
		SpecHash: metaHash, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	requested := cloneResolved(persisted)
	requested.Nodes[0].CPUs++
	manager := planFixtureManager(t)
	plan, err := manager.Plan(context.Background(), requested)
	if err != nil || plan.Action != "recreate" || !plan.Destructive {
		t.Fatalf("drift plan=%#v err=%v", plan, err)
	}
	if _, err := manager.Up(context.Background(), requested); !errors.Is(err, ErrRecreateRequired) {
		t.Fatalf("drift up error=%T %v, want ErrRecreateRequired", err, err)
	}
	if after, err := store.ReadDeployment(); err != nil || after.SpecHash != persistedHash {
		t.Fatalf("drift planning/up mutated deployment: %#v err=%v", after, err)
	}
}

func TestPrivatePlanStopsBeforeMutationOnNetworkMismatch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("FARROW_HOME", root)
	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{
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
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("network mismatch created the data root: %v", err)
	}
}

func TestRestartAndRecreatePreflightBeforeLifecycleMutation(t *testing.T) {
	for _, command := range []string{"restart", "recreate"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("FARROW_HOME", root)
			resolved := singlePrivateResolved()
			hash, err := spec.Hash(resolved)
			if err != nil {
				t.Fatal(err)
			}
			store := state.Store{Root: root}
			if err := store.WriteDeployment(state.DeploymentState{Schema: state.DeploymentSchema, FarrowVersion: "test", SpecHash: hash, Resolved: resolved, UpdatedAt: time.Unix(1, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			profile, err := platform.Resolve("darwin", "arm64")
			if err != nil {
				t.Fatal(err)
			}
			preflightCalls := 0
			manager := Manager{
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
			if _, err := store.ReadDeployment(); err != nil {
				t.Fatalf("command=%s mutated/destroyed deployment state after failed preflight: %v", command, err)
			}
		})
	}
}

type rejectingPrivateShareRunner struct {
	binaries []string
}

func (runner *rejectingPrivateShareRunner) Run(_ context.Context, binary string, args ...string) (execx.Result, error) {
	if len(args) != 2 || args[0] != "-device" || args[1] != "help" {
		return execx.Result{}, fmt.Errorf("unexpected command %s %v", binary, args)
	}
	runner.binaries = append(runner.binaries, binary)
	return execx.Result{Stdout: []byte("virtio-net-pci\n")}, nil
}

func privateShareFixture(t *testing.T) spec.Share {
	t.Helper()
	source := filepath.Join(t.TempDir(), "share-source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	return spec.Share{Host: canonical, Guest: "/mnt/source", Readonly: true}
}

func persistPrivateShare(t *testing.T, store state.Store, share spec.Share) state.DeploymentState {
	t.Helper()
	projectState, err := store.ReadDeployment()
	if err != nil {
		t.Fatal(err)
	}
	projectState.Resolved.Nodes[0].Shares = []spec.Share{share}
	projectState.SpecHash, err = spec.Hash(projectState.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteDeployment(projectState); err != nil {
		t.Fatal(err)
	}
	node, err := store.ReadNode(projectState.Resolved.Nodes[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	node.SpecHash = projectState.SpecHash
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	return projectState
}

func privateShareCapabilityManager(t *testing.T, fixture StartConfig, runner execx.Runner) Manager {
	t.Helper()
	profile, err := platform.Resolve("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	return Manager{
		Runner:        runner,
		NativeProfile: func() (platform.Profile, error) { return profile, nil },
		NetworkPreflight: func(context.Context, platform.Profile, netpreflight.Request, execx.Runner) netpreflight.Report {
			return netpreflight.Report{Ready: true}
		},
		HostPreflight: func(context.Context, platform.Profile, *spec.PrivateNetwork, execx.Runner) (Backend, error) {
			return Backend{}, nil
		},
		LookPath: func(string) (string, error) { return "/fixture/qemu-system-aarch64", nil },
	}
}

func assertPrivateShareCapabilityFailurePreservesState(t *testing.T, fixture StartConfig, beforeNode state.NodeState, err error) {
	t.Helper()
	var capability *CapabilityError
	if !errors.As(err, &capability) {
		t.Fatalf("error=%T %v, want CapabilityError", err, err)
	}
	store := state.Store{Root: fixture.Project.Root}
	afterNode, readErr := store.ReadNode(beforeNode.Node)
	if readErr != nil || afterNode.Phase != beforeNode.Phase || afterNode.SpecHash != beforeNode.SpecHash {
		t.Fatalf("node mutated after failed share preflight: before=%#v after=%#v err=%v", beforeNode, afterNode, readErr)
	}
	if _, readErr := store.ReadDeployment(); readErr != nil {
		t.Fatalf("deployment state disappeared after failed share preflight: %v", readErr)
	}
}

func TestPrivateRestartShareCapabilityPrecedesStop(t *testing.T) {
	fixture, _ := preparedStartFixture(t)
	t.Setenv("FARROW_HOME", fixture.Project.Root)
	store := state.Store{Root: fixture.Project.Root}
	persistPrivateShare(t, store, privateShareFixture(t))
	beforeNode, err := store.ReadNode("meta")
	if err != nil {
		t.Fatal(err)
	}
	runner := &rejectingPrivateShareRunner{}
	_, err = privateShareCapabilityManager(t, fixture, runner).Restart(context.Background())
	assertPrivateShareCapabilityFailurePreservesState(t, fixture, beforeNode, err)
	if len(runner.binaries) != 1 || runner.binaries[0] != beforeNode.Invocation.Binary {
		t.Fatalf("share probes=%v, want persisted binary %q", runner.binaries, beforeNode.Invocation.Binary)
	}
}

func TestPrivateRecreateShareCapabilityPrecedesDestroy(t *testing.T) {
	for _, test := range []struct {
		name           string
		currentShare   bool
		requestedShare bool
		wantBinary     string
	}{
		{name: "add-share", requestedShare: true, wantBinary: "/fixture/qemu-system-aarch64"},
		{name: "remove-share", currentShare: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, _ := preparedStartFixture(t)
			t.Setenv("FARROW_HOME", fixture.Project.Root)
			store := state.Store{Root: fixture.Project.Root}
			share := privateShareFixture(t)
			projectState, err := store.ReadDeployment()
			if err != nil {
				t.Fatal(err)
			}
			if test.currentShare {
				projectState = persistPrivateShare(t, store, share)
			}
			requested := cloneResolved(projectState.Resolved)
			if test.requestedShare {
				requested.Nodes[0].Shares = []spec.Share{share}
			} else {
				requested.Nodes[0].Shares = nil
			}
			beforeNode, err := store.ReadNode("meta")
			if err != nil {
				t.Fatal(err)
			}
			runner := &rejectingPrivateShareRunner{}
			_, err = privateShareCapabilityManager(t, fixture, runner).RecreateResolved(context.Background(), requested)
			assertPrivateShareCapabilityFailurePreservesState(t, fixture, beforeNode, err)
			wantBinary := test.wantBinary
			if wantBinary == "" {
				wantBinary = beforeNode.Invocation.Binary
			}
			if len(runner.binaries) != 1 || runner.binaries[0] != wantBinary {
				t.Fatalf("share probes=%v, want %q", runner.binaries, wantBinary)
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

func TestPrivateMaterializesDataRootFromEnvironment(t *testing.T) {
	root := t.TempDir()
	desired := singlePrivateResolved()
	desired.DataRoot = filepath.Join(root, "configured")
	t.Setenv("FARROW_HOME", filepath.Join(root, "environment"))
	materialized, err := (Manager{}).materializeDataRoot(desired)
	if err != nil || materialized.DataRoot != filepath.Join(root, "environment") {
		t.Fatalf("materialized = %#v, %v", materialized, err)
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

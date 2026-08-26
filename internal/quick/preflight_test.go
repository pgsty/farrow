package quick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/platform"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

type quickVersionRunner struct {
	result   execx.Result
	calls    int
	binaries []string
}

func (runner *quickVersionRunner) Run(_ context.Context, binary string, args ...string) (execx.Result, error) {
	runner.calls++
	runner.binaries = append(runner.binaries, binary)
	if len(args) != 1 || args[0] != "--version" {
		return execx.Result{}, errors.New("unexpected command")
	}
	return runner.result, nil
}

func TestQuickQEMUPreflightRejectsVersionBelowProfileMinimum(t *testing.T) {
	t.Parallel()
	profile := platform.Profile{MinimumQEMU: platform.Version{Major: 8, Minor: 2, Patch: 1}}
	runner := &quickVersionRunner{result: execx.Result{Stderr: []byte("QEMU emulator version 8.2.0\n")}}
	err := quickQEMUPreflight(context.Background(), profile, "/opt/qemu/bin/qemu-system-aarch64", false, runner)
	var capability *CapabilityError
	if !errors.As(err, &capability) || !strings.Contains(err.Error(), "minimum is 8.2.1") {
		t.Fatalf("version preflight error = %T %v", err, err)
	}
	if runner.calls != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.calls)
	}
	if runner.binaries[0] != "/opt/qemu/bin/qemu-system-aarch64" {
		t.Fatalf("version probe binary = %q", runner.binaries[0])
	}
}

func TestQuickQEMUPreflightAcceptsConfiguredMinimum(t *testing.T) {
	t.Parallel()
	profile := platform.Profile{MinimumQEMU: platform.Version{Major: 6, Minor: 2}}
	runner := &quickVersionRunner{result: execx.Result{Stdout: []byte("QEMU emulator version 6.2.0\n")}}
	if err := quickQEMUPreflight(context.Background(), profile, "/usr/bin/qemu-system-x86_64", false, runner); err != nil {
		t.Fatalf("minimum version rejected: %v", err)
	}
}

type quickShareCapabilityRunner struct {
	deviceHelp   string
	versionCalls int
	deviceCalls  int
}

func (runner *quickShareCapabilityRunner) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	switch {
	case len(args) == 1 && args[0] == "--version":
		runner.versionCalls++
		return execx.Result{Stdout: []byte("QEMU emulator version 99.0.0\n")}, nil
	case len(args) == 2 && args[0] == "-device" && args[1] == "help":
		runner.deviceCalls++
		return execx.Result{Stdout: []byte(runner.deviceHelp)}, nil
	default:
		return execx.Result{}, errors.New("unexpected command")
	}
}

func TestQuickQEMUPreflightRequiresExactShareDevice(t *testing.T) {
	t.Parallel()
	profile := platform.Profile{MinimumQEMU: platform.Version{Major: 6, Minor: 2}}
	for _, test := range []struct {
		name       string
		deviceHelp string
		wantError  bool
	}{
		{name: "supported", deviceHelp: `name "virtio-9p-pci", bus PCI`},
		{name: "missing", deviceHelp: `name "virtio-net-pci", bus PCI`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &quickShareCapabilityRunner{deviceHelp: test.deviceHelp}
			err := quickQEMUPreflight(context.Background(), profile, "/usr/bin/qemu-system-x86_64", true, runner)
			if test.wantError {
				assertQuickCapabilityError(t, err)
			} else if err != nil {
				t.Fatal(err)
			}
			if runner.versionCalls != 1 || runner.deviceCalls != 1 {
				t.Fatalf("preflight calls version=%d device-help=%d, want 1 each", runner.versionCalls, runner.deviceCalls)
			}
		})
	}
}

func assertQuickCapabilityError(t *testing.T, err error) {
	t.Helper()
	var capability *CapabilityError
	if !errors.As(err, &capability) {
		t.Fatalf("QEMU preflight error = %T %v", err, err)
	}
}

func assertQuickNodeUnchanged(t *testing.T, fixture reconcileFixture, before state.NodeState) {
	t.Helper()
	after, err := fixture.store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("QEMU capability preflight changed node state: before=%#v after=%#v", before, after)
	}
}

func addQuickShareToFixture(t *testing.T, fixture *reconcileFixture) spec.Resolved {
	t.Helper()
	source := t.TempDir()
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved := fixture.projectState.Resolved
	resolved.Nodes[0].Shares = []spec.Share{{Host: source, Guest: "/src", Readonly: true}}
	specHash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	projectState := fixture.projectState
	projectState.Resolved = resolved
	projectState.SpecHash = specHash
	projectState.UpdatedAt = time.Now().UTC()
	node := fixture.node
	node.SpecHash = specHash
	node.UpdatedAt = projectState.UpdatedAt
	node.Invocation, err = buildInvocation(fixture.store.Project, projectState, node)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WriteProject(projectState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	fixture.projectState = projectState
	fixture.node = node
	return resolved
}

func TestQuickStartVersionGatePrecedesStateMutation(t *testing.T) {
	t.Parallel()
	fixture := newReconcileFixture(t)
	runner := &quickVersionRunner{result: execx.Result{Stdout: []byte("QEMU emulator version 0.0.0\n")}}
	fixture.manager.Runner = runner
	before, err := fixture.store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.manager.Start(context.Background())
	assertQuickCapabilityError(t, err)
	assertQuickNodeUnchanged(t, fixture, before)
	if runner.calls != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.calls)
	}
}

func TestQuickRestartVersionGatePrecedesStop(t *testing.T) {
	t.Parallel()
	fixture := newReconcileFixture(t)
	node := fixture.node
	node.Phase = state.Running
	if err := fixture.store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	runner := &quickVersionRunner{result: execx.Result{Stdout: []byte("QEMU emulator version 0.0.0\n")}}
	fixture.manager.Runner = runner
	before, err := fixture.store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.manager.Restart(context.Background())
	assertQuickCapabilityError(t, err)
	assertQuickNodeUnchanged(t, fixture, before)
	if runner.calls != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.calls)
	}
}

func TestQuickUpRestartDriftVersionGatePrecedesStateMutation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	projectValue, err := project.Create(root, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	resolved := spec.Quick(true, true)
	specHash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store := state.Store{Project: projectValue}
	if err := store.WriteProject(state.ProjectState{
		Schema: state.ProjectSchema, FarrowVersion: "test", ProjectID: projectValue.Marker.ProjectID,
		SpecHash: specHash, Resolved: resolved, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	node := state.NodeState{
		Schema: state.NodeSchema, FarrowVersion: "test", ProjectID: projectValue.Marker.ProjectID,
		Node: nodeName, VMUUID: "00000000-0000-4000-8000-000000000001", Phase: state.Running,
		Generation: 1, SpecHash: specHash, Invocation: qemu.Invocation{Binary: "/persisted/qemu"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}
	desired := resolved
	desired.Nodes[0].CPUs++
	runner := &quickVersionRunner{result: execx.Result{Stdout: []byte("QEMU emulator version 0.0.0\n")}}
	manager := Manager{
		CWD: root, Runner: runner,
		NativeProfile: func() (platform.Profile, error) {
			return platform.Profile{QEMUBinary: "qemu-system-test", MinimumQEMU: platform.Version{Major: 6, Minor: 2}}, nil
		},
		LookPath: func(name string) (string, error) {
			if name != "qemu-system-test" {
				t.Fatalf("look path name = %q", name)
			}
			return "/test/bin/qemu-system-test", nil
		},
	}
	before, err := store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.UpResolvedWithPolicy(context.Background(), desired, UpPolicy{Restart: true})
	assertQuickCapabilityError(t, err)
	after, readErr := store.ReadNode(nodeName)
	if readErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed drift restart changed state: before=%#v after=%#v err=%v", before, after, readErr)
	}
	if runner.calls != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.calls)
	}
	if runner.binaries[0] != "/test/bin/qemu-system-test" {
		t.Fatalf("version probe binary = %q", runner.binaries[0])
	}
}

func TestQuickFreshUpVersionGatePrecedesProjectMutation(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	runner := &quickVersionRunner{result: execx.Result{Stdout: []byte("QEMU emulator version 0.0.0\n")}}
	manager := Manager{
		CWD: workDir, Runner: runner,
		NativeProfile: func() (platform.Profile, error) {
			return platform.Profile{QEMUBinary: "qemu-system-test", MinimumQEMU: platform.Version{Major: 6, Minor: 2}}, nil
		},
		LookPath: func(name string) (string, error) {
			if name != "qemu-system-test" {
				t.Fatalf("look path name = %q", name)
			}
			return "/test/bin/qemu-system-test", nil
		},
	}
	_, err := manager.Up(context.Background())
	assertQuickCapabilityError(t, err)
	if _, statErr := os.Lstat(filepath.Join(workDir, ".farrow")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed fresh Up mutated project marker: %v", statErr)
	}
	if runner.calls != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.calls)
	}
	if runner.binaries[0] != "/test/bin/qemu-system-test" {
		t.Fatalf("version probe binary = %q", runner.binaries[0])
	}
}

func TestQuickShareDeviceGatePrecedesLifecycleMutation(t *testing.T) {
	for _, command := range []string{"up", "start", "restart", "recreate"} {
		t.Run(command, func(t *testing.T) {
			fixture := newReconcileFixture(t)
			resolved := addQuickShareToFixture(t, &fixture)
			runner := &quickShareCapabilityRunner{deviceHelp: `name "virtio-net-pci", bus PCI`}
			fixture.manager.Runner = runner
			before, err := fixture.store.ReadNode(nodeName)
			if err != nil {
				t.Fatal(err)
			}
			switch command {
			case "up":
				_, err = fixture.manager.Up(context.Background())
			case "start":
				_, err = fixture.manager.Start(context.Background())
			case "restart":
				_, err = fixture.manager.Restart(context.Background())
			case "recreate":
				_, err = fixture.manager.RecreateResolved(context.Background(), resolved)
			}
			assertQuickCapabilityError(t, err)
			assertQuickNodeUnchanged(t, fixture, before)
			if runner.versionCalls != 1 || runner.deviceCalls != 1 {
				t.Fatalf("preflight calls version=%d device-help=%d, want 1 each", runner.versionCalls, runner.deviceCalls)
			}
		})
	}
}

func TestQuickExistingLaunchDoesNotProbeUnvalidatedInvocation(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"start", "restart"} {
		command := command
		t.Run(command, func(t *testing.T) {
			fixture := newReconcileFixture(t)
			node := fixture.node
			node.Invocation.Binary = "/untrusted/persisted/qemu"
			if err := fixture.store.WriteNode(node); err != nil {
				t.Fatal(err)
			}
			runner := &quickVersionRunner{result: execx.Result{Stdout: []byte("QEMU emulator version 99.0.0\n")}}
			fixture.manager.Runner = runner
			var err error
			if command == "start" {
				_, err = fixture.manager.Start(context.Background())
			} else {
				_, err = fixture.manager.Restart(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), "persisted QEMU invocation") {
				t.Fatalf("unvalidated invocation error = %v", err)
			}
			if runner.calls != 0 {
				t.Fatalf("unvalidated persisted binary triggered %d runner calls", runner.calls)
			}
		})
	}
}

type launchRejectingVersionRunner struct {
	versionCalls int
}

func (runner *launchRejectingVersionRunner) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	if len(args) == 1 && args[0] == "--version" {
		runner.versionCalls++
		return execx.Result{Stdout: []byte("QEMU emulator version 99.0.0\n")}, nil
	}
	return execx.Result{}, errors.New("stop after launch evidence check")
}

func TestQuickRestartLaunchReusesSingleVersionProbe(t *testing.T) {
	t.Parallel()
	fixture := newReconcileFixture(t)
	runner := &launchRejectingVersionRunner{}
	fixture.manager.Runner = runner
	preflight, err := fixture.manager.preflightExistingQEMU(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.startExisting(context.Background(), preflight); err == nil {
		t.Fatal("fake launch unexpectedly succeeded")
	}
	if runner.versionCalls != 1 {
		t.Fatalf("version probe calls = %d, want 1", runner.versionCalls)
	}
}

func TestQuickRestartLaunchReusesSingleShareDeviceProbe(t *testing.T) {
	fixture := newReconcileFixture(t)
	addQuickShareToFixture(t, &fixture)
	runner := &quickShareCapabilityRunner{deviceHelp: `name "virtio-9p-pci", bus PCI`}
	fixture.manager.Runner = runner
	preflight, err := fixture.manager.preflightExistingQEMU(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.startExisting(context.Background(), preflight); err == nil {
		t.Fatal("fake extra-file launch unexpectedly succeeded")
	}
	if runner.versionCalls != 1 || runner.deviceCalls != 1 {
		t.Fatalf("preflight calls version=%d device-help=%d, want 1 each", runner.versionCalls, runner.deviceCalls)
	}
}

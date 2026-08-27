package vm

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/qmp"
)

func TestSSHArgsAreIsolated(t *testing.T) {
	t.Parallel()
	joined := strings.Join(SSHArgsForUser("dba", "/key", "/known", 2222, "true"), " ")
	for _, expected := range []string{"-F /dev/null", "IdentitiesOnly=yes", "StrictHostKeyChecking=accept-new", `UserKnownHostsFile="/known"`, "dba@127.0.0.1 'true'"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("SSH args missing %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "StrictHostKeyChecking=no") {
		t.Fatal("host key checking disabled")
	}
}

func TestSSHArgsPreserveKnownHostsPathWithSpaces(t *testing.T) {
	t.Parallel()
	knownHosts := filepath.Join(t.TempDir(), "Application Support", "farrow", "known_hosts")
	args := SSHArgsForUser("dba", "/key", knownHosts, 2222)
	wantOption := `UserKnownHostsFile="` + knownHosts + `"`
	found := false
	for _, argument := range args {
		found = found || argument == wantOption
	}
	if !found {
		t.Fatalf("quoted known_hosts option missing: %#v", args)
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH client unavailable")
	}
	output, err := exec.Command(sshPath, append([]string{"-G"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh -G rejected quoted path: %v\n%s", err, output)
	}
	wantEffective := "userknownhostsfile " + knownHosts
	if !strings.Contains(strings.ToLower(string(output)), strings.ToLower(wantEffective)) {
		t.Fatalf("ssh -G split or changed known_hosts path; want %q\n%s", wantEffective, output)
	}
}

func TestQuoteRemotePreservesBoundaries(t *testing.T) {
	t.Parallel()
	got := QuoteRemote([]string{"sh", "-c", "printf '%s' \"$1\"", "sh", "a b;$(touch nope)"})
	want := `'sh' '-c' 'printf '"'"'%s'"'"' "$1"' 'sh' 'a b;$(touch nope)'`
	if got != want {
		t.Fatalf("quoted command:\n got %s\nwant %s", got, want)
	}
}

func TestSSHArgsUseValidatedResolvedUser(t *testing.T) {
	t.Parallel()
	joined := strings.Join(SSHArgsForUser("operator", "/key", "/known", 2222, "true"), " ")
	if !strings.Contains(joined, "operator@127.0.0.1") || strings.Contains(joined, "dba@127.0.0.1") {
		t.Fatalf("custom SSH user was not preserved: %s", joined)
	}
	if args := SSHArgsForUser("-oProxyCommand=bad", "/key", "/known", 2222); args != nil {
		t.Fatalf("unsafe SSH user produced args: %v", args)
	}
}

type lifecycleRunner struct{}

func (lifecycleRunner) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{}, errors.New("unexpected runner call")
}

type qmpResult[T any] struct {
	value T
	err   error
}

type lifecycleQMP struct {
	mu             sync.Mutex
	names          []qmpResult[qmp.Name]
	uuids          []qmpResult[qmp.UUID]
	powerdownError error
	quitError      error
	calls          []string
}

func popQMPResult[T any](values *[]qmpResult[T]) (T, error) {
	if len(*values) == 0 {
		var zero T
		return zero, errors.New("unscripted QMP response")
	}
	result := (*values)[0]
	*values = (*values)[1:]
	return result.value, result.err
}

func (fake *lifecycleQMP) QueryName(context.Context, string) (qmp.Name, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "query-name")
	return popQMPResult(&fake.names)
}

func (fake *lifecycleQMP) QueryUUID(context.Context, string) (qmp.UUID, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "query-uuid")
	return popQMPResult(&fake.uuids)
}

func (fake *lifecycleQMP) Powerdown(context.Context, string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "powerdown")
	return fake.powerdownError
}

func (fake *lifecycleQMP) Quit(context.Context, string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls = append(fake.calls, "quit")
	return fake.quitError
}

func (fake *lifecycleQMP) recordedCalls() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.calls...)
}

type stopRecorder struct {
	mu           sync.Mutex
	matches      []bool
	waits        []bool
	signals      []syscall.Signal
	waitTimeouts []time.Duration
}

func (recorder *stopRecorder) match(context.Context, execx.Runner, process.Identity, qemu.Invocation) bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.matches) == 0 {
		return false
	}
	matched := recorder.matches[0]
	recorder.matches = recorder.matches[1:]
	return matched
}

func (recorder *stopRecorder) wait(_ context.Context, _ int, timeout time.Duration) (bool, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.waitTimeouts = append(recorder.waitTimeouts, timeout)
	if len(recorder.waits) == 0 {
		return false, errors.New("unscripted process wait")
	}
	exited := recorder.waits[0]
	recorder.waits = recorder.waits[1:]
	return exited, nil
}

func (recorder *stopRecorder) signal(_ int, signal syscall.Signal) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.signals = append(recorder.signals, signal)
	return nil
}

func (recorder *stopRecorder) recordedSignals() []syscall.Signal {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]syscall.Signal(nil), recorder.signals...)
}

func (recorder *stopRecorder) recordedWaits() []time.Duration {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]time.Duration(nil), recorder.waitTimeouts...)
}

const (
	testVMName = "node-1"
	testVMUUID = "01234567-89ab-4def-8123-0123456789ab"
)

var (
	testIdentity   = process.Identity{PID: 4242, Executable: "/opt/qemu-system-aarch64", Started: "stable-start", ArgvHash: "stable-argv"}
	testInvocation = qemu.Invocation{Binary: "/opt/qemu-system-aarch64", Args: []string{"-name", testVMName}}
)

func matchingQMPIdentities(count int) ([]qmpResult[qmp.Name], []qmpResult[qmp.UUID]) {
	names := make([]qmpResult[qmp.Name], count)
	uuids := make([]qmpResult[qmp.UUID], count)
	for index := 0; index < count; index++ {
		names[index].value.Name = testVMName
		uuids[index].value.UUID = testVMUUID
	}
	return names, uuids
}

func testStopLifecycle(fake QMPClient, recorder *stopRecorder) Lifecycle {
	return Lifecycle{
		Runner:         lifecycleRunner{},
		QMP:            fake,
		matchesProcess: recorder.match,
		processAlive:   func(int) bool { return true },
		signalProcess:  recorder.signal,
		waitProcess:    recorder.wait,
		quitTimeout:    23 * time.Millisecond,
		termTimeout:    29 * time.Millisecond,
		killTimeout:    31 * time.Millisecond,
	}
}

func TestStopUsesQMPPowerdownWithoutSignalWhenGuestExits(t *testing.T) {
	t.Parallel()
	names, uuids := matchingQMPIdentities(1)
	fake := &lifecycleQMP{names: names, uuids: uuids}
	recorder := &stopRecorder{matches: []bool{true}, waits: []bool{true}}
	lifecycle := testStopLifecycle(fake, recorder)

	if err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, 17*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.recordedCalls(), ","); got != "query-name,query-uuid,powerdown" {
		t.Fatalf("QMP calls = %s", got)
	}
	if signals := recorder.recordedSignals(); len(signals) != 0 {
		t.Fatalf("signals = %v", signals)
	}
	if waits := recorder.recordedWaits(); len(waits) != 1 || waits[0] != 17*time.Millisecond {
		t.Fatalf("waits = %v", waits)
	}
}

func TestStopPreservesPowerdownThenQuitOrder(t *testing.T) {
	t.Parallel()
	names, uuids := matchingQMPIdentities(2)
	fake := &lifecycleQMP{names: names, uuids: uuids}
	recorder := &stopRecorder{matches: []bool{true, true}, waits: []bool{false, true}}
	lifecycle := testStopLifecycle(fake, recorder)

	if err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, 17*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	want := "query-name,query-uuid,powerdown,query-name,query-uuid,quit"
	if got := strings.Join(fake.recordedCalls(), ","); got != want {
		t.Fatalf("QMP calls = %s, want %s", got, want)
	}
	if signals := recorder.recordedSignals(); len(signals) != 0 {
		t.Fatalf("signals = %v", signals)
	}
	if waits := recorder.recordedWaits(); len(waits) != 2 || waits[0] != 17*time.Millisecond || waits[1] != 23*time.Millisecond {
		t.Fatalf("waits = %v", waits)
	}
}

func TestStopQMPUnavailableUsesVerifiedTERMThenKILL(t *testing.T) {
	t.Parallel()
	fake := &lifecycleQMP{names: []qmpResult[qmp.Name]{{err: errors.New("socket unavailable")}}}
	recorder := &stopRecorder{matches: []bool{true, true}, waits: []bool{false, true}}
	lifecycle := testStopLifecycle(fake, recorder)

	if err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := recorder.recordedSignals(); len(got) != 2 || got[0] != syscall.SIGTERM || got[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v", got)
	}
	if waits := recorder.recordedWaits(); len(waits) != 2 || waits[0] != 29*time.Millisecond || waits[1] != 31*time.Millisecond {
		t.Fatalf("waits = %v", waits)
	}
}

func TestStopQMPIdentityMismatchRefusesEverySignal(t *testing.T) {
	t.Parallel()
	fake := &lifecycleQMP{
		names: []qmpResult[qmp.Name]{{value: qmp.Name{Name: "different-vm"}}},
		uuids: []qmpResult[qmp.UUID]{{value: qmp.UUID{UUID: testVMUUID}}},
	}
	recorder := &stopRecorder{}
	lifecycle := testStopLifecycle(fake, recorder)

	err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, time.Second)
	if err == nil || !errors.Is(err, ErrQMPIdentityMismatch) {
		t.Fatalf("error = %v", err)
	}
	if signals := recorder.recordedSignals(); len(signals) != 0 {
		t.Fatalf("signals = %v", signals)
	}
}

func TestStopQMPUnavailableRefusesUnknownProcessIdentity(t *testing.T) {
	t.Parallel()
	fake := &lifecycleQMP{names: []qmpResult[qmp.Name]{{err: errors.New("socket unavailable")}}}
	recorder := &stopRecorder{matches: []bool{false}}
	lifecycle := testStopLifecycle(fake, recorder)

	err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, process.Identity{PID: testIdentity.PID}, testInvocation, time.Second)
	if err == nil || !strings.Contains(err.Error(), "incomplete process identity") {
		t.Fatalf("error = %v", err)
	}
	if signals := recorder.recordedSignals(); len(signals) != 0 {
		t.Fatalf("signals = %v", signals)
	}
}

func TestStopQMPUnavailableRefusesInvocationMismatch(t *testing.T) {
	t.Parallel()
	fake := &lifecycleQMP{names: []qmpResult[qmp.Name]{{err: errors.New("socket unavailable")}}}
	recorder := &stopRecorder{matches: []bool{false}}
	lifecycle := testStopLifecycle(fake, recorder)

	err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, qemu.Invocation{Binary: testInvocation.Binary, Args: []string{"-name", "different-vm"}}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "without matching process identity") {
		t.Fatalf("error = %v", err)
	}
	if signals := recorder.recordedSignals(); len(signals) != 0 {
		t.Fatalf("signals = %v", signals)
	}
}

func TestStopRefusesKILLWhenIdentityChangesAfterTERM(t *testing.T) {
	t.Parallel()
	fake := &lifecycleQMP{names: []qmpResult[qmp.Name]{{err: errors.New("socket unavailable")}}}
	recorder := &stopRecorder{matches: []bool{true, false}, waits: []bool{false}}
	lifecycle := testStopLifecycle(fake, recorder)

	err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, time.Second)
	if err == nil || !strings.Contains(err.Error(), "refuse SIGKILL") {
		t.Fatalf("error = %v", err)
	}
	if got := recorder.recordedSignals(); len(got) != 1 || got[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v", got)
	}
}

func TestStopPowerdownFailureDoesNotSignalWhileQMPAvailable(t *testing.T) {
	t.Parallel()
	names, uuids := matchingQMPIdentities(2)
	fake := &lifecycleQMP{names: names, uuids: uuids, powerdownError: errors.New("powerdown rejected")}
	recorder := &stopRecorder{matches: []bool{true}}
	lifecycle := testStopLifecycle(fake, recorder)

	err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, time.Second)
	if err == nil || !strings.Contains(err.Error(), "matching QMP remained available") {
		t.Fatalf("error = %v", err)
	}
	if signals := recorder.recordedSignals(); len(signals) != 0 {
		t.Fatalf("signals = %v", signals)
	}
}

func TestStopAfterQuitFallsBackOnlyAfterQMPBecomesUnavailable(t *testing.T) {
	t.Parallel()
	names, uuids := matchingQMPIdentities(2)
	names = append(names, qmpResult[qmp.Name]{err: errors.New("socket closed")})
	fake := &lifecycleQMP{names: names, uuids: uuids}
	recorder := &stopRecorder{matches: []bool{true, true, true}, waits: []bool{false, false, true}}
	lifecycle := testStopLifecycle(fake, recorder)

	if err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, 17*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	wantCalls := "query-name,query-uuid,powerdown,query-name,query-uuid,quit,query-name"
	if got := strings.Join(fake.recordedCalls(), ","); got != wantCalls {
		t.Fatalf("QMP calls = %s, want %s", got, wantCalls)
	}
	if got := recorder.recordedSignals(); len(got) != 1 || got[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v", got)
	}
}

func TestStopReportsBoundedKILLWaitExhaustion(t *testing.T) {
	t.Parallel()
	fake := &lifecycleQMP{names: []qmpResult[qmp.Name]{{err: errors.New("socket unavailable")}}}
	recorder := &stopRecorder{matches: []bool{true, true}, waits: []bool{false, false}}
	lifecycle := testStopLifecycle(fake, recorder)

	err := lifecycle.Stop(context.Background(), "/qmp", testVMName, testVMUUID, testIdentity, testInvocation, time.Second)
	if err == nil || !strings.Contains(err.Error(), "bounded SIGKILL wait") {
		t.Fatalf("error = %v", err)
	}
	if got := recorder.recordedSignals(); len(got) != 2 || got[0] != syscall.SIGTERM || got[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v", got)
	}
}

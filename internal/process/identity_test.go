package process

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/qemu"
)

func testRunner() execx.Runner { return execx.OSRunner{Timeout: 10 * time.Second} }

// liveProcess starts a long-lived child and returns its PID and the invocation
// a caller would have recorded for it.
// The argument stands in for the per-node `-uuid` that makes every real Farrow
// QEMU command line unique.
func liveProcess(t *testing.T, seconds string) (int, qemu.Invocation) {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is not installed")
	}
	command := exec.Command(sleep, seconds)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	return command.Process.Pid, qemu.Invocation{Binary: sleep, Args: []string{seconds}}
}

func TestAliveDistinguishesRealProcesses(t *testing.T) {
	t.Parallel()
	if !Alive(os.Getpid()) {
		t.Fatal("the running test process is not reported alive")
	}
	for _, pid := range []int{0, -1, -12345} {
		if Alive(pid) {
			t.Errorf("Alive(%d) = true, want false", pid)
		}
	}
	// A PID above the platform maximum can never name a live process, so no
	// signal can reach it.
	if Alive(1 << 30) {
		t.Error("an impossible PID was reported alive")
	}
}

func TestCaptureRecordsStableIdentityAndRejectsAForeignBinary(t *testing.T) {
	t.Parallel()
	pid, invocation := liveProcess(t, "121")

	identity, err := Capture(context.Background(), testRunner(), invocation, pid)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != pid || identity.Executable == "" || identity.Started == "" || len(identity.ArgvHash) != 64 {
		t.Fatalf("identity = %#v", identity)
	}
	// Capture must be a pure observation: two reads of an unchanged process have
	// to agree, or every later liveness check would report a false mismatch.
	second, err := Capture(context.Background(), testRunner(), invocation, pid)
	if err != nil || second != identity {
		t.Fatalf("Capture is not stable: %#v vs %#v (%v)", second, identity, err)
	}

	// A PID that has been recycled into someone else's program must not be
	// accepted as ours just because the number matches.
	foreign := qemu.Invocation{Binary: "/usr/local/bin/qemu-system-aarch64", Args: invocation.Args}
	if _, err := Capture(context.Background(), testRunner(), foreign, pid); err == nil {
		t.Fatal("Capture accepted a process running a different executable")
	}
}

func TestCaptureRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	for name, pid := range map[string]int{"zero": 0, "negative": -1} {
		if _, err := Capture(context.Background(), testRunner(), qemu.Invocation{Binary: "/bin/sleep"}, pid); err == nil {
			t.Errorf("Capture accepted a %s PID", name)
		}
	}
	if _, err := Capture(context.Background(), nil, qemu.Invocation{Binary: "/bin/sleep"}, os.Getpid()); err == nil {
		t.Error("Capture accepted a nil runner")
	}
}

func TestMatchesLiveRequiresEveryRecordedFact(t *testing.T) {
	t.Parallel()
	pid, invocation := liveProcess(t, "121")
	identity, err := Capture(context.Background(), testRunner(), invocation, pid)
	if err != nil {
		t.Fatal(err)
	}
	if !MatchesLive(context.Background(), testRunner(), identity, invocation) {
		t.Fatal("a freshly captured live process did not match itself")
	}

	// Each recorded fact is load-bearing. A state file missing any one of them
	// must not be trusted to identify a process we are about to signal.
	for name, mutate := range map[string]func(Identity) Identity{
		"no executable": func(i Identity) Identity { i.Executable = ""; return i },
		"no start time": func(i Identity) Identity { i.Started = ""; return i },
		"no argv hash":  func(i Identity) Identity { i.ArgvHash = ""; return i },
		"wrong argv hash": func(i Identity) Identity {
			i.ArgvHash = "0000000000000000000000000000000000000000000000000000000000000000"
			return i
		},
		"wrong start time": func(i Identity) Identity { i.Started = "Thu Jan  1 00:00:00 1970"; return i },
		"dead pid":         func(i Identity) Identity { i.PID = 1 << 30; return i },
	} {
		if MatchesLive(context.Background(), testRunner(), mutate(identity), invocation) {
			t.Errorf("MatchesLive accepted an identity with %s", name)
		}
	}
}

func TestMatchesLiveRejectsARecycledPID(t *testing.T) {
	t.Parallel()
	pid, invocation := liveProcess(t, "121")
	identity, err := Capture(context.Background(), testRunner(), invocation, pid)
	if err != nil {
		t.Fatal(err)
	}
	// The number still names a live process, but it is running a different
	// command line. This is the case that would otherwise get an innocent
	// process killed, and the argv hash is what rules it out: every real QEMU
	// command line carries that node's own `-uuid`, so no two Farrow guests —
	// and no unrelated program — ever hash the same.
	other, otherInvocation := liveProcess(t, "122")
	identity.PID = other
	if MatchesLive(context.Background(), testRunner(), identity, otherInvocation) {
		t.Fatal("MatchesLive accepted a different live process under a reused PID")
	}
}

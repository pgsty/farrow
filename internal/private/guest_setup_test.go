package private

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pgsty/farrow/internal/cloudinit"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/vm"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/execx"
)

func TestGuestRepairOwnsItsDeadline(t *testing.T) {
	runner := guestSetupRunner(execx.OSRunner{Timeout: time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, "/bin/sh", "-c", "sleep 0.05; printf repaired"); err != nil {
		t.Fatalf("host probe deadline killed repair: %v", err)
	}
	cancel()
	if _, err := runner.Run(ctx, "/bin/sh", "-c", "sleep 1"); err == nil {
		t.Fatal("repair ignored cancellation")
	}
}

func TestGuestSetupErrorRetainsCauseWithoutEncodedPayload(t *testing.T) {
	command := &execx.CommandError{Binary: "ssh", Args: []string{strings.Repeat("encoded-script", 1000)}, ExitCode: 1, Stderr: "guest setup already running"}
	err := guestSetupError(command)
	var typed *GuestSetupError
	if !errors.As(err, &typed) || !strings.Contains(err.Error(), command.Stderr) || len(err.Error()) > 250 {
		t.Fatalf("bad error: %v", err)
	}
	if len(command.Args[0]) < 1000 {
		t.Fatal("mutated original diagnostic")
	}
}

type setupRetryRunner struct {
	node     state.NodeState
	warnings []state.GuestWarning
	old      bool
	scripts  []string
	calls    int
}

func (r *setupRetryRunner) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	r.calls++
	command := strings.Join(args, " ")
	if strings.Contains(command, "base64 -d") {
		r.scripts = append(r.scripts, command)
		r.warnings = nil
		r.old = false
		return execx.Result{}, nil
	}
	version := cloudinit.SetupVersion
	if r.old {
		version = ""
	}
	data, err := json.Marshal(map[string]any{"node": r.node.Node, "generation": r.node.Generation, "spec_hash": r.node.SpecHash, "setup_version": version, "warnings": r.warnings})
	return execx.Result{Stdout: data}, err
}

func TestUpRetriesOnlyIncompleteGuestSetup(t *testing.T) {
	for _, scenario := range []string{"healthy", "limited", "old-helper", "start"} {
		t.Run(scenario, func(t *testing.T) {
			public, _ := testSSHKeyPair(t)
			key := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(key+".pub", []byte(public), 0600); err != nil {
				t.Fatal(err)
			}
			node := state.NodeState{Node: "meta", VMUUID: "fixture", Generation: 1, SpecHash: strings.Repeat("a", 64), SSHPort: 2222}
			runner := &setupRetryRunner{node: node, old: scenario == "old-helper"}
			if scenario == "limited" || scenario == "start" {
				runner.warnings = []state.GuestWarning{{Stage: "shares", Detail: "read-only"}}
			}
			lifecycle := NativeLifecycle{RetryGuestSetup: scenario != "start", Resolved: privateResolved(), VM: vm.Lifecycle{Runner: runner, SSHUser: "dba"}, PrivateKey: key, KnownHosts: key + ".known", SSHPath: "ssh"}
			warnings, err := lifecycle.WaitReady(context.Background(), node, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "healthy" || scenario == "start" {
				if runner.calls != 1 || len(runner.scripts) != 0 {
					t.Fatalf("healthy/start reran setup: %+v", runner)
				}
			} else {
				if len(runner.scripts) != 1 || len(warnings) != 0 {
					t.Fatalf("setup not recovered: %+v %v", runner, warnings)
				}
				if scenario == "limited" && !strings.Contains(runner.scripts[0], "farrow-finalize shares ready") {
					t.Fatal("reran healthy setup stages")
				}
			}
		})
	}
}

package linux

import (
	"context"
	"testing"

	"github.com/pgsty/farrow/internal/execx"
)

type unitStateRunner struct{ output string }

func (runner unitStateRunner) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{Stdout: []byte(runner.output)}, nil
}

func TestOptionalNetworkManagerMayBeAbsent(t *testing.T) {
	output := "LoadState=not-found\nActiveState=inactive\nSubState=dead\nUnitFileState=\n"
	state, err := discoverOptionalUnitState(context.Background(), unitStateRunner{output: output}, "NetworkManager.service")
	if err != nil || state.LoadState != "not-found" || state.ActiveState != "inactive" {
		t.Fatalf("optional state=%#v err=%v", state, err)
	}
	if _, err := discoverUnitStateMode(context.Background(), unitStateRunner{output: output}, "systemd-networkd.service", false); err == nil {
		t.Fatal("required systemd unit accepted a not-found state")
	}
}

func TestOptionalNetworkManagerStillRejectsMalformedState(t *testing.T) {
	output := "LoadState=not-found\nActiveState=active\nSubState=running\nUnitFileState=\n"
	if _, err := discoverOptionalUnitState(context.Background(), unitStateRunner{output: output}, "NetworkManager.service"); err == nil {
		t.Fatal("malformed optional unit state was accepted")
	}
}

package quick

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/state"
)

func TestIntegrationRepairReconstructsLiveProcess(t *testing.T) {
	workDir := os.Getenv("FARROW_REPAIR_E2E_PROJECT")
	if workDir == "" {
		t.Skip("set FARROW_REPAIR_E2E_PROJECT to an existing stopped quick project")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	manager := Manager{CWD: workDir, FarrowVersion: "integration-test", ReadyTimeout: 3 * time.Minute}
	if _, err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cleanupCancel()
		_, _ = manager.Repair(cleanupCtx, true)
		_, _ = manager.Stop(cleanupCtx)
	}()

	projectValue, err := project.Open(workDir)
	if err != nil {
		t.Fatal(err)
	}
	store := state.Store{Project: projectValue}
	node, err := store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	originalIdentity := node.Process
	if !process.MatchesLive(ctx, manager.runner(), processFromState(originalIdentity), node.Invocation) {
		t.Fatal("started VM does not have a verified process identity")
	}

	// Simulate a CLI crash after QEMU and its pidfile are live but before the
	// final running/process identity state write commits.
	node.Phase = state.Starting
	node.Process = state.ProcessIdentity{}
	node.UpdatedAt = time.Now().UTC()
	if err := store.WriteNode(node); err != nil {
		t.Fatal(err)
	}

	dryRun, err := manager.Repair(ctx, false)
	if err != nil || len(dryRun.Actions) != 1 || dryRun.Actions[0].Kind != "update-state" || dryRun.Actions[0].Applied {
		t.Fatalf("dry-run repair = %#v, %v", dryRun, err)
	}
	stillBroken, err := store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	if stillBroken.Phase != state.Starting || stillBroken.Process != (state.ProcessIdentity{}) {
		t.Fatalf("dry run mutated state: %#v", stillBroken)
	}

	applied, err := manager.Repair(ctx, true)
	if err != nil || len(applied.Actions) != 1 || !applied.Actions[0].Applied {
		t.Fatalf("applied repair = %#v, %v", applied, err)
	}
	recovered, err := store.ReadNode(nodeName)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != state.Running || recovered.Process != originalIdentity {
		t.Fatalf("recovered state = phase=%s process=%#v; want running %#v", recovered.Phase, recovered.Process, originalIdentity)
	}
	if !process.MatchesLive(ctx, manager.runner(), processFromState(recovered.Process), recovered.Invocation) {
		t.Fatal("recovered process identity does not match the live QEMU process")
	}
}

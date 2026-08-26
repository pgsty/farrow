package private

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/state"
)

func completePrivateProcessIdentity(node state.NodeState) bool {
	return node.Process.PID > 0 && node.Process.Executable != "" && node.Process.Started != "" && node.Process.ArgvHash != "" && node.Invocation.Binary != ""
}

func completePrivateRuntimeIdentity(node state.NodeState) bool {
	return filepath.IsAbs(node.Runtime.Directory) && node.Runtime.QMP == filepath.Join(node.Runtime.Directory, "qmp.sock") && node.Runtime.PIDFile == filepath.Join(node.Runtime.Directory, "qemu.pid")
}

func statusNodesRunning(status Status) bool {
	if len(status.Nodes) == 0 {
		return false
	}
	for _, node := range status.Nodes {
		if node.State != state.Running || node.Runtime != "running" {
			return false
		}
	}
	return true
}

func privateLeaseNode(node state.NodeState) lease.Node {
	return lease.Node{
		Name: node.Node, VMUUID: node.VMUUID,
		Runtime:    lease.RuntimePaths{Directory: node.Runtime.Directory, QMP: node.Runtime.QMP, PIDFile: node.Runtime.PIDFile},
		Invocation: node.Invocation,
		Process: process.Identity{
			PID: node.Process.PID, Executable: node.Process.Executable,
			Started: node.Process.Started, ArgvHash: node.Process.ArgvHash,
		},
	}
}

type statusLeaseSyncPlan struct {
	active   bool
	store    lease.Store
	original lease.Lease
	desired  lease.Lease
	auditor  lease.RuntimeAuditor
}

// planStatusLeaseSyncLocked proves that the same project's host-global lease
// may mirror the proposed durable node states without mutating it. In
// particular, every Running -> Stopped lease transition is audited before the
// caller writes any node state.
func (m Manager) planStatusLeaseSyncLocked(ctx context.Context, projectValue project.Project, nodes []state.NodeState) (statusLeaseSyncPlan, error) {
	leaseStore := m.leaseStore()
	leaseStatus, err := leaseStore.Inspect()
	if err != nil || !leaseStatus.Active || leaseStatus.Lease == nil {
		return statusLeaseSyncPlan{}, err
	}
	active := *leaseStatus.Lease
	if active.ProjectID != projectValue.Marker.ProjectID || active.OwnerUID != os.Getuid() {
		return statusLeaseSyncPlan{}, nil
	}
	desired, err := SynchronizeLease(active, nodes)
	if err != nil {
		return statusLeaseSyncPlan{}, err
	}
	auditor := lease.RuntimeIdentityAuditor(m.runner(), time.Second)
	for index := range desired.Nodes {
		if desired.Nodes[index].Phase != lease.Stopped || reflect.DeepEqual(active.Nodes[index], desired.Nodes[index]) {
			continue
		}
		observation, err := auditor(ctx, active.Nodes[index])
		if err != nil {
			return statusLeaseSyncPlan{}, err
		}
		if observation.Live {
			return statusLeaseSyncPlan{}, fmt.Errorf("refuse converge private lease node %s to stopped: %s", observation.Node, observation.Evidence)
		}
		// Keep the last complete process evidence in the stopped lease until
		// Release audits it again. This closes the race between the pre-update
		// proof and the final lease deletion.
		if active.Nodes[index].Process.PID > 0 {
			desired.Nodes[index].Process = active.Nodes[index].Process
		}
	}
	return statusLeaseSyncPlan{active: true, store: leaseStore, original: active, desired: desired, auditor: auditor}, nil
}

// applyStatusLeaseSyncLocked mirrors an already audited convergence plan. It
// deliberately leaves runtime files for repair. An all-stopped lease is
// removed only through the ordinary audited release path, which rechecks QMP,
// recorded process identity, and any pidfile.
func applyStatusLeaseSyncLocked(ctx context.Context, projectID string, plan statusLeaseSyncPlan) error {
	if !plan.active {
		return nil
	}
	current := plan.desired
	if !reflect.DeepEqual(plan.original.Nodes, plan.desired.Nodes) {
		updated, err := plan.store.Update(ctx, plan.desired)
		if err != nil {
			return err
		}
		current = updated.Lease
	}
	for _, node := range current.Nodes {
		if node.Phase != lease.Stopped {
			return nil
		}
	}
	_, err := plan.store.Release(ctx, projectID, true, plan.auditor)
	return err
}

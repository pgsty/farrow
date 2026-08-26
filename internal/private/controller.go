package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/project"
)

type PartialError struct {
	Nodes      []string
	RolledBack []string
}

func (e *PartialError) Error() string {
	if len(e.RolledBack) != 0 {
		return fmt.Sprintf("private multi-node operation partially succeeded; failed nodes: %v; safely rolled back prepare artifacts: %v", e.Nodes, e.RolledBack)
	}
	return fmt.Sprintf("private multi-node operation partially succeeded; failed nodes: %v", e.Nodes)
}

type Controller struct {
	Project      project.Project
	LeaseStore   lease.Store
	Prepare      PrepareConfig
	Lifecycle    NodeLifecycle
	Concurrency  int
	ReadyTimeout time.Duration
	NoWait       bool
	CreateNodes  []string
	StartNodes   []string
	SetupRuntime func(string) error
	Version      string
}

type CreateResult struct {
	Lease   lease.Lease      `json:"lease"`
	Prepare []PrepareOutcome `json:"prepare"`
	Commit  CommitResult     `json:"commit"`
	Start   []StartOutcome   `json:"start"`
}

type FailedRollback struct {
	Node   string         `json:"node"`
	Result RollbackResult `json:"result"`
}

func reservedRuntimeAuditor(_ context.Context, node lease.Node) (lease.Observation, error) {
	if node.Phase != lease.Reserved && node.Phase != lease.Prepared && node.Phase != lease.Stopped {
		return lease.Observation{}, fmt.Errorf("refuse pre-start cleanup for node %s phase %s", node.Name, node.Phase)
	}
	for _, pathname := range []string{node.Runtime.QMP, node.Runtime.PIDFile} {
		if pathname == "" {
			continue
		}
		if _, err := os.Lstat(pathname); err == nil {
			return lease.Observation{}, fmt.Errorf("refuse pre-start cleanup with runtime artifact %s", pathname)
		} else if !errors.Is(err, os.ErrNotExist) {
			return lease.Observation{}, err
		}
	}
	return lease.Observation{Node: node.Name, Authority: "dead", Evidence: "pre-start reservation has no QMP or pidfile"}, nil
}

func (controller Controller) releasePreStartLease(ctx context.Context, value lease.Lease) error {
	_, err := controller.LeaseStore.Abort(ctx, value.ProjectID, true, reservedRuntimeAuditor)
	return err
}

func withLeaseCleanup(operationErr, cleanupErr error) error {
	if cleanupErr == nil {
		return operationErr
	}
	return fmt.Errorf("%w; pre-start lease cleanup failed: %v", operationErr, cleanupErr)
}

func failedCreateNodes(result CreateResult) []string {
	failed := make(map[string]struct{})
	for _, outcome := range result.Prepare {
		if outcome.Error != "" {
			failed[outcome.Node] = struct{}{}
		}
	}
	for _, node := range result.Commit.Failed {
		failed[node] = struct{}{}
	}
	for _, outcome := range result.Start {
		if outcome.Error != "" || !outcome.Ready {
			failed[outcome.Node] = struct{}{}
		}
	}
	resultNames := make([]string, 0, len(failed))
	for name := range failed {
		resultNames = append(resultNames, name)
	}
	sort.Strings(resultNames)
	return resultNames
}

func (controller Controller) CreateAndStart(ctx context.Context) (CreateResult, error) {
	result := CreateResult{}
	if controller.Project.Root == "" || controller.Prepare.ProjectRoot != controller.Project.Root || controller.Prepare.Plan.ProjectID != controller.Project.Marker.ProjectID || controller.Lifecycle == nil || controller.Version == "" {
		return result, fmt.Errorf("private controller project, prepare, lifecycle, or version is incomplete")
	}
	acquired, err := controller.LeaseStore.Acquire(ctx, controller.Prepare.Plan.Lease)
	if err != nil {
		return result, err
	}
	result.Lease = acquired.Lease
	cleanupLease := func(cleanupErr error) error {
		if acquired.Action == "reentered" {
			return cleanupErr
		}
		return withLeaseCleanup(cleanupErr, controller.releasePreStartLease(ctx, result.Lease))
	}
	lockContext, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	projectLock, err := lock.Acquire(lockContext, filepath.Join(controller.Project.Root, "project.lock"), false)
	if err != nil {
		return result, cleanupLease(err)
	}
	defer projectLock.Release()
	createNames, err := selectedNodeNames(controller.Prepare.Resolved, controller.CreateNodes)
	if err != nil {
		return result, cleanupLease(err)
	}
	result.Prepare = PrepareSelected(ctx, controller.Prepare, createNames, controller.Concurrency)
	result.Commit, err = CommitPrepared(ctx, controller.Project, controller.Prepare, result.Prepare, controller.Version)
	if err != nil {
		return result, cleanupLease(err)
	}
	if len(result.Commit.Nodes) == 0 {
		failed := failedCreateNodes(result)
		partial := &PartialError{Nodes: failed}
		return result, cleanupLease(partial)
	}
	desiredLease, err := SynchronizeLease(result.Lease, result.Commit.Nodes)
	if err != nil {
		return result, err
	}
	updated, err := controller.LeaseStore.Update(ctx, desiredLease)
	if err != nil {
		return result, cleanupLease(err)
	}
	result.Lease = updated.Lease
	for _, node := range result.Commit.Nodes {
		if err := FinalizePrepared(controller.Project, node.Node, LeaseNodeVerifier(result.Lease, lease.Prepared)); err != nil {
			return result, withLeaseCleanup(err, controller.releasePreStartLease(ctx, result.Lease))
		}
	}
	startNames := make([]string, 0, len(result.Commit.Nodes))
	startAll := len(controller.StartNodes) == 0
	selected := nodeNameSet(controller.StartNodes)
	// Manager supplies the complete requested create set as StartNodes. A node
	// that failed offline prepare is therefore still present in that selection,
	// but it has no committed state and must not prevent successfully prepared
	// peers from starting. Keep rejecting genuinely out-of-scope selections by
	// removing only names that CommitPrepared classified as failed.
	for _, node := range result.Commit.Failed {
		delete(selected, node)
	}
	for _, node := range result.Commit.Nodes {
		if startAll {
			startNames = append(startNames, node.Node)
		} else if _, include := selected[node.Node]; include {
			startNames = append(startNames, node.Node)
			delete(selected, node.Node)
		}
	}
	if len(selected) != 0 {
		return result, fmt.Errorf("private controller start selection contains nodes outside committed project")
	}
	if len(startNames) != 0 {
		result.Start, result.Lease, err = StartPrepared(ctx, StartConfig{
			Project: controller.Project, LeaseStore: controller.LeaseStore, Lifecycle: controller.Lifecycle,
			Nodes: startNames, Concurrency: controller.Concurrency, ReadyTimeout: controller.ReadyTimeout, NoWait: controller.NoWait,
			SetupRuntime: controller.SetupRuntime,
		})
		if err != nil {
			return result, err
		}
	}
	failed := failedCreateNodes(result)
	if len(failed) > 0 {
		return result, &PartialError{Nodes: failed}
	}
	return result, nil
}

func RollbackFailedPrepares(projectValue project.Project, result CreateResult, apply bool) ([]FailedRollback, error) {
	rollbacks := make([]FailedRollback, 0)
	for _, outcome := range result.Prepare {
		if outcome.Error == "" {
			continue
		}
		rollback, err := RollbackPrepared(projectValue, outcome.Node, apply)
		if err != nil {
			return rollbacks, err
		}
		rollbacks = append(rollbacks, FailedRollback{Node: outcome.Node, Result: rollback})
	}
	return rollbacks, nil
}

func rollbackCreateFailure(projectValue project.Project, result CreateResult, operationErr error) error {
	rollbacks, err := RollbackFailedPrepares(projectValue, result, true)
	if err != nil {
		return fmt.Errorf("%w; requested failed-prepare rollback also failed: %v", operationErr, err)
	}
	rolledBack := make([]string, 0, len(rollbacks))
	for _, rollback := range rollbacks {
		rolledBack = append(rolledBack, rollback.Node)
	}
	var partial *PartialError
	if errors.As(operationErr, &partial) {
		partial.RolledBack = rolledBack
	}
	return operationErr
}

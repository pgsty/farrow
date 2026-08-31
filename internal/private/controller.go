package private

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/lock"
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
	Project      Deployment
	Prepare      PrepareConfig
	Lifecycle    NodeLifecycle
	Concurrency  int
	ReadyTimeout time.Duration
	NoWait       bool
	CreateNodes  []string
	StartNodes   []string
	SetupRuntime func(string) error
	Version      string
	Progress     activity.Reporter
}

type CreateResult struct {
	Prepare []PrepareOutcome `json:"prepare"`
	Commit  CommitResult     `json:"commit"`
	Start   []StartOutcome   `json:"start"`
}

type FailedRollback struct {
	Node   string         `json:"node"`
	Result RollbackResult `json:"result"`
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
	if controller.Project.Root == "" || controller.Prepare.ProjectRoot != controller.Project.Root || controller.Lifecycle == nil || controller.Version == "" {
		return result, fmt.Errorf("private controller deployment, prepare, lifecycle, or version is incomplete")
	}
	lockContext, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	projectLock, err := acquireDeploymentLock(lockContext, controller.Project.Root, false)
	if err != nil {
		return result, err
	}
	defer func() {
		err = lock.JoinRelease(err, projectLock, "deployment controller lock")
	}()
	createNames, err := selectedNodeNames(controller.Prepare.Resolved, controller.CreateNodes)
	if err != nil {
		return result, err
	}
	controller.Progress.Report(activity.Event{Phase: "prepare", Message: fmt.Sprintf("Preparing disks and cloud-init for %d node(s)", len(createNames))})
	result.Prepare = PrepareSelected(ctx, controller.Prepare, createNames, controller.Concurrency)
	controller.Progress.Report(activity.Event{Phase: "prepare", Message: fmt.Sprintf("Prepared %d of %d node(s)", len(PreparedNames(result.Prepare)), len(createNames)), Done: true})
	controller.Progress.Report(activity.Event{Phase: "commit", Message: "Committing prepared node state"})
	result.Commit, err = CommitPrepared(controller.Project, controller.Prepare, result.Prepare, controller.Version)
	if err != nil {
		return result, err
	}
	if len(result.Commit.Nodes) == 0 {
		failed := failedCreateNodes(result)
		return result, &PartialError{Nodes: failed}
	}
	for _, node := range result.Commit.Nodes {
		if err := FinalizePrepared(controller.Project, node.Node); err != nil {
			return result, err
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
		return result, fmt.Errorf("private controller start selection contains nodes outside the committed deployment")
	}
	if len(startNames) != 0 {
		startMessage := fmt.Sprintf("Starting %d node(s) and waiting up to %s for guest readiness", len(startNames), controller.ReadyTimeout)
		if controller.NoWait {
			startMessage = fmt.Sprintf("Starting %d node(s) without waiting for guest readiness", len(startNames))
		}
		controller.Progress.Report(activity.Event{Phase: "guest-ready", Message: startMessage})
		result.Start, err = StartPrepared(ctx, StartConfig{
			Project: controller.Project, Lifecycle: controller.Lifecycle,
			Nodes: startNames, Concurrency: controller.Concurrency, ReadyTimeout: controller.ReadyTimeout, NoWait: controller.NoWait,
			SetupRuntime: controller.SetupRuntime,
		})
		if err != nil {
			return result, err
		}
		readyMessage := fmt.Sprintf("All %d node(s) are ready", len(startNames))
		if controller.NoWait {
			readyMessage = fmt.Sprintf("QEMU is running for %d node(s); guest readiness was skipped", len(startNames))
		}
		controller.Progress.Report(activity.Event{Phase: "guest-ready", Message: readyMessage, Done: true})
	}
	failed := failedCreateNodes(result)
	if len(failed) > 0 {
		return result, &PartialError{Nodes: failed}
	}
	return result, nil
}

func RollbackFailedPrepares(projectValue Deployment, result CreateResult, apply bool) ([]FailedRollback, error) {
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

func rollbackCreateFailure(projectValue Deployment, result CreateResult, operationErr error) error {
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

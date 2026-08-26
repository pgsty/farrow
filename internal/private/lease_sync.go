package private

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/pgsty/farrow/internal/lease"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/state"
)

func leasePhase(phase state.Phase) (lease.NodePhase, error) {
	switch phase {
	case state.Prepared:
		return lease.Prepared, nil
	case state.Starting:
		return lease.Starting, nil
	case state.Running:
		return lease.Running, nil
	case state.Stopping:
		return lease.Stopping, nil
	case state.Stopped:
		return lease.Stopped, nil
	default:
		return "", fmt.Errorf("state phase %s cannot be mirrored into private lease", phase)
	}
}

func SynchronizeLease(existing lease.Lease, nodes []state.NodeState) (lease.Lease, error) {
	if existing.ProjectID == "" || len(existing.Nodes) == 0 {
		return lease.Lease{}, errors.New("active private lease is empty")
	}
	desired := existing
	desired.Nodes = append([]lease.Node(nil), existing.Nodes...)
	indices := make(map[string]int, len(desired.Nodes))
	for index, node := range desired.Nodes {
		indices[node.Name] = index
	}
	seen := make(map[string]struct{})
	for _, nodeState := range nodes {
		if nodeState.ProjectID != existing.ProjectID {
			return lease.Lease{}, fmt.Errorf("node %s project differs from active private lease", nodeState.Node)
		}
		index, ok := indices[nodeState.Node]
		if !ok {
			return lease.Lease{}, fmt.Errorf("node %s is absent from active private lease", nodeState.Node)
		}
		if _, duplicate := seen[nodeState.Node]; duplicate {
			return lease.Lease{}, fmt.Errorf("node %s appears twice in lease synchronization", nodeState.Node)
		}
		seen[nodeState.Node] = struct{}{}
		current := desired.Nodes[index]
		if current.VMUUID != nodeState.VMUUID {
			return lease.Lease{}, fmt.Errorf("node %s VM UUID differs from reservation", nodeState.Node)
		}
		phase, err := leasePhase(nodeState.Phase)
		if err != nil {
			return lease.Lease{}, err
		}
		current.Phase = phase
		current.Runtime = lease.RuntimePaths{Directory: nodeState.Runtime.Directory, QMP: nodeState.Runtime.QMP, PIDFile: nodeState.Runtime.PIDFile}
		current.Invocation = nodeState.Invocation
		current.Process = process.Identity{PID: nodeState.Process.PID, Executable: nodeState.Process.Executable, Started: nodeState.Process.Started, ArgvHash: nodeState.Process.ArgvHash}
		desired.Nodes[index] = current
	}
	return desired, nil
}

func LeaseNodeVerifier(active lease.Lease, expected lease.NodePhase) LeaseCommitVerifier {
	return func(nodeState state.NodeState) error {
		for _, leased := range active.Nodes {
			if leased.Name != nodeState.Node {
				continue
			}
			if leased.Phase != expected || leased.VMUUID != nodeState.VMUUID || leased.Runtime.Directory != nodeState.Runtime.Directory || leased.Runtime.QMP != nodeState.Runtime.QMP || leased.Runtime.PIDFile != nodeState.Runtime.PIDFile || !reflect.DeepEqual(leased.Invocation, nodeState.Invocation) {
				return fmt.Errorf("lease node %s does not match committed state/phase", nodeState.Node)
			}
			return nil
		}
		return fmt.Errorf("lease has no node %s", nodeState.Node)
	}
}

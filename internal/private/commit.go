package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

type CommitResult struct {
	Project state.DeploymentState `json:"deployment"`
	Nodes   []state.NodeState     `json:"nodes"`
	Failed  []string              `json:"failed,omitempty"`
}

func desiredDeploymentState(resolved spec.Resolved, hash, version string, now time.Time) state.DeploymentState {
	return state.DeploymentState{Schema: state.DeploymentSchema, FarrowVersion: version, SpecHash: hash, Resolved: resolved, UpdatedAt: now}
}

// envelopeOf strips the node list so project-level settings can be compared
// on their own. Any envelope change moves every node's hash and is therefore
// a whole-project recreate, never a silent in-place update.
func envelopeOf(value spec.Resolved) spec.Resolved {
	envelope := value
	if value.Private != nil {
		privateNetwork := *value.Private
		envelope.Private = &privateNetwork
	}
	envelope.Nodes = nil
	return envelope
}

// additiveResolvedChange reports whether desired differs from existing only
// by node additions, or by replacing definitions of nodes whose state was
// already destroyed (the per-node recreate window). Nodes may never disappear
// here — removal is an explicit destroy, not a state-commit side effect.
func additiveResolvedChange(store state.Store, existing, desired spec.Resolved) bool {
	if !reflect.DeepEqual(envelopeOf(existing), envelopeOf(desired)) {
		return false
	}
	desiredNodes := make(map[string]spec.Node, len(desired.Nodes))
	for _, node := range desired.Nodes {
		desiredNodes[node.Name] = node
	}
	for _, node := range existing.Nodes {
		if _, kept := desiredNodes[node.Name]; !kept {
			return false
		}
	}
	existingNodes := make(map[string]spec.Node, len(existing.Nodes))
	for _, node := range existing.Nodes {
		existingNodes[node.Name] = node
	}
	for _, node := range desired.Nodes {
		current, known := existingNodes[node.Name]
		if !known {
			continue
		}
		if reflect.DeepEqual(current, node) {
			continue
		}
		if _, err := store.ReadNode(node.Name); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func ensureProjectState(store state.Store, desired state.DeploymentState) error {
	existing, err := store.ReadDeployment()
	if errors.Is(err, os.ErrNotExist) {
		return store.WriteDeployment(desired)
	}
	if err != nil {
		return err
	}
	if existing.SpecHash == desired.SpecHash && reflect.DeepEqual(existing.Resolved, desired.Resolved) {
		return nil
	}
	if !additiveResolvedChange(store, existing.Resolved, desired.Resolved) {
		return errors.New("refuse private state commit over different resolved deployment state")
	}
	return store.WriteDeployment(desired)
}

func stateForArtifacts(config PrepareConfig, projectValue Deployment, artifacts NodeArtifacts, journal PrepareJournal, version string, now time.Time) (state.NodeState, error) {
	definition, ok := nodeSpec(config.Resolved, artifacts.Name)
	if !ok {
		return state.NodeState{}, fmt.Errorf("resolved spec has no node %s", artifacts.Name)
	}
	nodePlan, ok := config.Plan.Node(artifacts.Name)
	if !ok {
		return state.NodeState{}, fmt.Errorf("private plan has no node %s", artifacts.Name)
	}
	baseAlias := definition.Image
	if baseAlias == "" {
		baseAlias = config.Resolved.Image
	}
	base, ok := config.Bases[baseAlias]
	if !ok || base.Digest == "" || base.Release == "" || base.VirtualSize <= 0 {
		return state.NodeState{}, fmt.Errorf("private node %s image state metadata is incomplete", artifacts.Name)
	}
	if base.Alias == "" {
		base.Alias = baseAlias
	}
	if journal.Node != artifacts.Name || journal.VMUUID != nodePlan.VMUUID || journal.SpecHash != config.NodeHashes[artifacts.Name] || !journal.Prepared || !reflect.DeepEqual(journal.Invocation, artifacts.Invocation) {
		return state.NodeState{}, errors.New("private prepared journal does not match the artifacts/deployment intent")
	}
	dataState := make([]state.DataDisk, 0, len(artifacts.Data))
	for index, data := range artifacts.Data {
		if index >= len(definition.Disks) || definition.Disks[index].Name != data.Name {
			return state.NodeState{}, errors.New("private data artifact order/name does not match resolved spec")
		}
		dataState = append(dataState, state.DataDisk{Name: data.Name, Path: data.Path, Serial: data.Serial, Size: data.Size, Mount: data.Mount, Persistent: definition.Disks[index].Persistent})
	}
	forwards := []qemu.Forward{{Bind: "127.0.0.1", Host: config.SSHPorts[artifacts.Name], Guest: 22}}
	for _, forward := range definition.Forwards {
		forwards = append(forwards, qemu.Forward{Bind: forward.Bind, Host: forward.Host, Guest: forward.Guest})
	}
	return state.NodeState{
		Schema: state.NodeSchema, FarrowVersion: version,
		Node: artifacts.Name, VMUUID: nodePlan.VMUUID, Phase: state.Prepared, Generation: 1, SpecHash: config.NodeHashes[artifacts.Name],
		Image:    state.Image{Alias: base.Alias, Release: base.Release, Digest: base.Digest, VirtualSize: base.VirtualSize},
		RootDisk: artifacts.Root, DataDisks: dataState, Seed: artifacts.Seed, NVRAM: artifacts.NVRAM,
		SSHPort: config.SSHPorts[artifacts.Name], Forwards: forwards,
		Runtime:    state.RuntimePaths{Directory: nodePlan.Runtime.Directory, QMP: nodePlan.Runtime.QMP, PIDFile: nodePlan.Runtime.PIDFile},
		Invocation: artifacts.Invocation, CreatedAt: journal.StartedAt, UpdatedAt: now,
	}, nil
}

func CommitPrepared(ctx context.Context, projectValue Deployment, config PrepareConfig, outcomes []PrepareOutcome, version string) (CommitResult, error) {
	_ = ctx
	if projectValue.Root != config.ProjectRoot || version == "" {
		return CommitResult{}, errors.New("private commit config/version identity mismatch")
	}
	now := config.now()
	projectState := desiredDeploymentState(config.Resolved, config.SpecHash, version, now)
	store := state.Store{Root: projectValue.Root}
	if err := ensureProjectState(store, projectState); err != nil {
		return CommitResult{}, err
	}
	result := CommitResult{Project: projectState, Nodes: []state.NodeState{}}
	for _, outcome := range outcomes {
		if outcome.Error != "" || outcome.Artifacts == nil {
			result.Failed = append(result.Failed, outcome.Node)
			continue
		}
		artifacts := *outcome.Artifacts
		journal, err := ReadPrepareJournal(artifacts.Journal)
		if err != nil {
			return result, err
		}
		nodeState, err := stateForArtifacts(config, projectValue, artifacts, journal, version, now)
		if err != nil {
			return result, err
		}
		if existing, readErr := store.ReadNode(nodeState.Node); readErr == nil {
			if existing.VMUUID != nodeState.VMUUID || existing.SpecHash != nodeState.SpecHash || !reflect.DeepEqual(existing.Invocation, nodeState.Invocation) {
				return result, fmt.Errorf("refuse overwrite of different private node state %s", nodeState.Node)
			}
		} else if errors.Is(readErr, os.ErrNotExist) {
			if err := store.WriteNode(nodeState); err != nil {
				return result, err
			}
		} else {
			return result, readErr
		}
		journal.StateCommitted = true
		journal.StatePath = filepath.Join(artifacts.NodeDir, "state.json")
		journal.UpdatedAt = config.now()
		if err := writePrepareJournal(artifacts.Journal, journal); err != nil {
			return result, err
		}
		result.Nodes = append(result.Nodes, nodeState)
	}
	return result, nil
}

func FinalizePrepared(projectValue Deployment, node string) error {
	nodeDir, err := projectValue.NodeDir(node)
	if err != nil {
		return err
	}
	journalPath := filepath.Join(nodeDir, "private-prepare.json")
	journal, err := ReadPrepareJournal(journalPath)
	if err != nil {
		return err
	}
	if !journal.StateCommitted || journal.StatePath != filepath.Join(nodeDir, "state.json") {
		return errors.New("private prepare journal state is not committed")
	}
	nodeState, err := (state.Store{Root: projectValue.Root}).ReadNode(node)
	if err != nil {
		return err
	}
	if nodeState.VMUUID != journal.VMUUID || nodeState.SpecHash != journal.SpecHash || !reflect.DeepEqual(nodeState.Invocation, journal.Invocation) {
		return errors.New("private prepare journal and node state differ")
	}
	info, err := os.Lstat(journalPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private prepare journal became unsafe")
	}
	if err := os.Remove(journalPath); err != nil {
		return err
	}
	return fsutil.SyncDir(nodeDir)
}

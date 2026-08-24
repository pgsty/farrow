package private

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/qemu"
	"github.com/pgsty/piglet/internal/spec"
	"github.com/pgsty/piglet/internal/state"
)

type CommitResult struct {
	Project state.ProjectState `json:"project"`
	Nodes   []state.NodeState  `json:"nodes"`
	Failed  []string           `json:"failed,omitempty"`
}

func desiredProjectState(projectValue project.Project, resolved spec.Resolved, hash, version string, now time.Time) state.ProjectState {
	return state.ProjectState{Schema: state.ProjectSchema, PigletVersion: version, ProjectID: projectValue.Marker.ProjectID, SpecHash: hash, Resolved: resolved, UpdatedAt: now}
}

func ensureProjectState(store state.Store, desired state.ProjectState) error {
	existing, err := store.ReadProject()
	if errors.Is(err, os.ErrNotExist) {
		return store.WriteProject(desired)
	}
	if err != nil {
		return err
	}
	if existing.ProjectID != desired.ProjectID || existing.SpecHash != desired.SpecHash || !reflect.DeepEqual(existing.Resolved, desired.Resolved) {
		return errors.New("refuse private state commit over different resolved project state")
	}
	return nil
}

func stateForArtifacts(config PrepareConfig, projectValue project.Project, artifacts NodeArtifacts, journal PrepareJournal, version string, now time.Time) (state.NodeState, error) {
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
	if journal.ProjectID != projectValue.Marker.ProjectID || journal.Node != artifacts.Name || journal.VMUUID != nodePlan.VMUUID || journal.SpecHash != config.SpecHash || !journal.Prepared || !reflect.DeepEqual(journal.Invocation, artifacts.Invocation) {
		return state.NodeState{}, errors.New("private prepared journal does not match artifacts/project intent")
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
		Schema: state.NodeSchema, PigletVersion: version, ProjectID: projectValue.Marker.ProjectID,
		Node: artifacts.Name, VMUUID: nodePlan.VMUUID, Phase: state.Prepared, Generation: 1, SpecHash: config.SpecHash,
		Image:    state.Image{Alias: base.Alias, Release: base.Release, Digest: base.Digest, VirtualSize: base.VirtualSize},
		RootDisk: artifacts.Root, DataDisks: dataState, Seed: artifacts.Seed, NVRAM: artifacts.NVRAM,
		SSHPort: config.SSHPorts[artifacts.Name], Forwards: forwards,
		Runtime:    state.RuntimePaths{Directory: nodePlan.Runtime.Directory, QMP: nodePlan.Runtime.QMP, PIDFile: nodePlan.Runtime.PIDFile},
		Invocation: artifacts.Invocation, CreatedAt: journal.StartedAt, UpdatedAt: now,
	}, nil
}

func CommitPrepared(ctx context.Context, projectValue project.Project, config PrepareConfig, outcomes []PrepareOutcome, version string) (CommitResult, error) {
	_ = ctx
	if projectValue.Root != config.ProjectRoot || projectValue.Marker.ProjectID != config.Plan.ProjectID || version == "" {
		return CommitResult{}, errors.New("private commit project/config/version identity mismatch")
	}
	now := config.now()
	projectState := desiredProjectState(projectValue, config.Resolved, config.SpecHash, version, now)
	store := state.Store{Project: projectValue}
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

type LeaseCommitVerifier func(state.NodeState) error

func FinalizePrepared(projectValue project.Project, node string, verifyLease LeaseCommitVerifier) error {
	if verifyLease == nil {
		return errors.New("private prepare finalization requires lease verification")
	}
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
	nodeState, err := (state.Store{Project: projectValue}).ReadNode(node)
	if err != nil {
		return err
	}
	if nodeState.VMUUID != journal.VMUUID || nodeState.SpecHash != journal.SpecHash || !reflect.DeepEqual(nodeState.Invocation, journal.Invocation) {
		return errors.New("private prepare journal and node state differ")
	}
	if err := verifyLease(nodeState); err != nil {
		return err
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

package quick

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/cloudinit"
	"github.com/pgsty/farrow/internal/disk"
	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/identity"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/state"
)

func ensureChangedPortsAvailable(desired, existing spec.Resolved, sshPort uint16) error {
	owned := make(map[string]struct{})
	for _, forward := range qemuForwards(existing, sshPort) {
		owned[net.JoinHostPort(forward.Bind, fmt.Sprintf("%d", forward.Host))] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, forward := range qemuForwards(desired, sshPort) {
		key := net.JoinHostPort(forward.Bind, fmt.Sprintf("%d", forward.Host))
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("desired forwards reuse host listener %s", key)
		}
		seen[key] = struct{}{}
		if _, alreadyOwned := owned[key]; alreadyOwned {
			continue
		}
		listener, err := net.Listen("tcp", key)
		if err != nil {
			return fmt.Errorf("desired host listener %s is unavailable: %w", key, err)
		}
		if err := listener.Close(); err != nil {
			return err
		}
	}
	return nil
}

func reconcileSeedFiles(projectValue project.Project, desired state.ProjectState, node state.NodeState) (cloudinit.Files, error) {
	publicPath := filepath.Join(projectValue.Root, "keys", "id_ed25519.pub")
	info, err := os.Lstat(publicPath)
	if err != nil || !info.Mode().IsRegular() {
		return cloudinit.Files{}, errors.New("project public key is missing or unsafe")
	}
	publicKey, err := os.ReadFile(publicPath)
	if err != nil {
		return cloudinit.Files{}, err
	}
	mgmtMAC, err := identity.MAC(projectValue.Marker.ProjectID, node.Node, "management")
	if err != nil {
		return cloudinit.Files{}, err
	}
	cloudDisks := make([]cloudinit.Disk, 0, len(node.DataDisks))
	for index, dataDisk := range node.DataDisks {
		filesystem := "auto"
		if index < len(desired.Resolved.Nodes[0].Disks) && desired.Resolved.Nodes[0].Disks[index].Filesystem != "" {
			filesystem = desired.Resolved.Nodes[0].Disks[index].Filesystem
		}
		cloudDisks = append(cloudDisks, cloudinit.Disk{Serial: dataDisk.Serial, Mount: dataDisk.Mount, Filesystem: filesystem})
	}
	return cloudinit.Render(cloudinit.Input{
		ProjectID: projectValue.Marker.ProjectID, Node: node.Node, Hostname: node.Node,
		Generation: node.Generation, SpecHash: node.SpecHash, SSHUser: desired.Resolved.SSHUser,
		PublicKey: strings.TrimSpace(string(publicKey)), MgmtMAC: mgmtMAC, Disks: cloudDisks,
		Shares: cloudShares(desired.Resolved.Nodes[0].Shares),
	})
}

func seedMatches(pathname string, intent *state.ReconcileIntent) bool {
	info, err := os.Lstat(pathname)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	label, files, err := cloudinit.ReadISO(pathname)
	if err != nil || !strings.EqualFold(strings.TrimSpace(label), "CIDATA") {
		return false
	}
	generation := []byte(fmt.Sprintf("-g%d", intent.Node.Generation))
	hash := []byte(intent.Project.SpecHash)
	return bytes.Contains(files["meta-data"], generation) && bytes.Contains(files["user-data"], hash)
}

func validateReconcileIntent(store state.Store, current state.NodeState, transaction state.Transaction) error {
	intent := transaction.Reconcile
	if intent == nil {
		return errors.New("transaction has no reconcile intent")
	}
	nodeDir, err := store.Project.NodeDir(transaction.Node)
	if err != nil {
		return err
	}
	expectedStage := filepath.Join(nodeDir, ".seed.iso.next-"+transaction.OperationID)
	if intent.StagedSeed != expectedStage {
		return errors.New("reconcile staged seed path does not match operation identity")
	}
	if intent.Node.RootDisk != current.RootDisk || intent.Node.Seed != current.Seed || intent.Node.NVRAM != current.NVRAM || intent.Node.Runtime != current.Runtime || intent.Node.VMUUID != current.VMUUID || intent.Node.SSHPort != current.SSHPort || intent.Node.Image != current.Image || intent.Node.CreatedAt != current.CreatedAt {
		return errors.New("reconcile intent changes immutable node identity or artifact paths")
	}
	if intent.Node.RootDisk != filepath.Join(nodeDir, "root.qcow2") || intent.Node.Seed != filepath.Join(nodeDir, "seed.iso") || len(intent.Node.DataDisks) > 1 || len(intent.Project.Resolved.Nodes) != 1 {
		return errors.New("reconcile intent contains unsupported node layout")
	}
	if len(intent.Node.DataDisks) < len(current.DataDisks) {
		return errors.New("reconcile intent removes an existing data disk")
	}
	if len(intent.Node.DataDisks) == 1 {
		dataDisk := intent.Node.DataDisks[0]
		if dataDisk.Persistent && len(current.DataDisks) == 1 && current.DataDisks[0].Persistent {
			if dataDisk.Path != current.DataDisks[0].Path {
				return errors.New("reconcile changes retained persistent disk path")
			}
		} else if dataDisk.Path != filepath.Join(nodeDir, "data.qcow2") {
			return errors.New("reconcile data disk path is not the exact owned path")
		}
	}
	expectedInvocation, err := buildInvocation(store.Project, intent.Project, intent.Node)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expectedInvocation, intent.Node.Invocation) {
		return errors.New("reconcile invocation does not match desired typed state")
	}
	return nil
}

func (m Manager) reconcileDiskManager() (disk.Manager, error) {
	qemuImg := m.QEMUImg
	if qemuImg == "" {
		var err error
		qemuImg, err = exec.LookPath("qemu-img")
		if err != nil {
			return disk.Manager{}, err
		}
	}
	return disk.Manager{QEMUImg: qemuImg, Runner: m.runner()}, nil
}

func inspectDesiredDisk(ctx context.Context, manager disk.Manager, pathname string, targetSize int64, allowBacking bool) (int64, error) {
	fileInfo, err := os.Lstat(pathname)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("disk %s is missing or not a regular non-symlink file", pathname)
	}
	info, err := manager.Inspect(ctx, pathname)
	if err != nil {
		return 0, err
	}
	if err := disk.ValidateRuntime(info, allowBacking); err != nil {
		return 0, err
	}
	if info.VirtualSize > targetSize {
		return 0, fmt.Errorf("disk %s is larger than desired; shrink is forbidden", pathname)
	}
	return info.VirtualSize, nil
}

func currentDisksMatch(ctx context.Context, manager disk.Manager, projectState state.ProjectState, node state.NodeState) bool {
	if len(projectState.Resolved.Nodes) != 1 {
		return false
	}
	rootSize, err := inspectDesiredDisk(ctx, manager, node.RootDisk, projectState.Resolved.Nodes[0].RootDisk, true)
	if err != nil || rootSize != projectState.Resolved.Nodes[0].RootDisk || len(node.DataDisks) != len(projectState.Resolved.Nodes[0].Disks) {
		return false
	}
	for index, dataDisk := range node.DataDisks {
		size, err := inspectDesiredDisk(ctx, manager, dataDisk.Path, projectState.Resolved.Nodes[0].Disks[index].Size, false)
		if err != nil || size != projectState.Resolved.Nodes[0].Disks[index].Size {
			return false
		}
	}
	return true
}

func addApplied(report *RepairReport, kind, pathname, reason string, apply bool, operation func() error) error {
	if apply && operation != nil {
		if err := operation(); err != nil {
			return err
		}
	}
	report.add(kind, pathname, reason, false, apply)
	return nil
}

func (m Manager) repairReconcile(ctx context.Context, store state.Store, current state.NodeState, transaction state.Transaction, report *RepairReport, apply bool) error {
	if err := validateReconcileIntent(store, current, transaction); err != nil {
		return blockRepair(report, err.Error())
	}
	if current.Phase != state.Stopped && current.Phase != state.Prepared {
		return blockRepair(report, "reconcile recovery requires a stopped/prepared persisted node")
	}
	if err := lifecycle(m.runner()).ValidateIdentity(ctx, current.Runtime.QMP, current.Node, current.VMUUID); err == nil {
		return blockRepair(report, "reconcile recovery refuses a QMP-verified running VM")
	}
	if process.MatchesLive(ctx, m.runner(), processFromState(current.Process), current.Invocation) {
		return blockRepair(report, "reconcile recovery refuses a matching live QEMU process")
	}
	if current.Process.PID > 0 && process.Alive(current.Process.PID) {
		return blockRepair(report, "reconcile recovery refuses an unverified live recorded PID")
	}
	manager, err := m.reconcileDiskManager()
	if err != nil {
		return err
	}
	intent := transaction.Reconcile
	stageInfo, stageErr := os.Lstat(intent.StagedSeed)
	stageExists := stageErr == nil
	if stageExists && (!stageInfo.Mode().IsRegular() || stageInfo.Mode()&os.ModeSymlink != 0 || !seedMatches(intent.StagedSeed, intent)) {
		return blockRepair(report, "staged reconcile seed is unsafe or does not match desired generation/hash")
	}
	if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
		return stageErr
	}
	finalMatches := seedMatches(intent.Node.Seed, intent)
	if !stageExists && !finalMatches {
		projectState, projectErr := store.ReadProject()
		if projectErr != nil || projectState.SpecHash != current.SpecHash || validateNodePaths(store.Project, current) != nil || validateInvocation(store.Project, projectState, current) != nil || !currentDisksMatch(ctx, manager, projectState, current) {
			return blockRepair(report, "reconcile seed is missing and current state cannot be proven untouched")
		}
		return removeTransactionFile(filepath.Dir(current.RootDisk), "reconcile staging never completed and current state/disks remain unchanged", report, apply)
	}

	desiredRootSize := intent.Project.Resolved.Nodes[0].RootDisk
	rootSize, err := inspectDesiredDisk(ctx, manager, intent.Node.RootDisk, desiredRootSize, true)
	if err != nil {
		return blockRepair(report, err.Error())
	}
	if rootSize < desiredRootSize {
		err = addApplied(report, "grow-root-disk", intent.Node.RootDisk, "offline qcow2 growth required by desired spec", apply, func() error {
			_, _, growErr := manager.Grow(ctx, intent.Node.RootDisk, desiredRootSize, true)
			return growErr
		})
		if err != nil {
			return err
		}
	}
	if len(intent.Node.DataDisks) == 1 {
		dataDisk := intent.Node.DataDisks[0]
		info, statErr := os.Lstat(dataDisk.Path)
		if errors.Is(statErr, os.ErrNotExist) {
			err = addApplied(report, "create-data-disk", dataDisk.Path, "new data disk required by desired spec", apply, func() error {
				_, createErr := manager.CreateBlank(ctx, dataDisk.Path, dataDisk.Size)
				return createErr
			})
			if err != nil {
				return err
			}
		} else if statErr != nil || !info.Mode().IsRegular() {
			return blockRepair(report, "desired data disk path is unsafe")
		} else {
			dataSize, inspectErr := inspectDesiredDisk(ctx, manager, dataDisk.Path, dataDisk.Size, false)
			if inspectErr != nil {
				return blockRepair(report, inspectErr.Error())
			}
			if dataSize < dataDisk.Size {
				err = addApplied(report, "grow-data-disk", dataDisk.Path, "offline qcow2 growth required by desired spec", apply, func() error {
					_, _, growErr := manager.Grow(ctx, dataDisk.Path, dataDisk.Size, false)
					return growErr
				})
				if err != nil {
					return err
				}
			}
		}
	}
	if stageExists {
		err = addApplied(report, "replace-seed", intent.Node.Seed, "publish desired generation-aware CIDATA atomically", apply, func() error {
			if err := os.Rename(intent.StagedSeed, intent.Node.Seed); err != nil {
				return err
			}
			return fsutil.SyncDir(filepath.Dir(intent.Node.Seed))
		})
		if err != nil {
			return err
		}
	}
	currentProject, projectErr := store.ReadProject()
	if projectErr != nil || !reflect.DeepEqual(currentProject, intent.Project) {
		if err := addApplied(report, "write-project-state", filepath.Join(store.Project.Root, "resolved.json"), "commit desired resolved spec/hash", apply, func() error { return store.WriteProject(intent.Project) }); err != nil {
			return err
		}
	}
	currentNode, nodeErr := store.ReadNode(current.Node)
	if nodeErr != nil || !reflect.DeepEqual(currentNode, intent.Node) {
		if err := addApplied(report, "write-node-state", filepath.Join(filepath.Dir(current.RootDisk), "state.json"), "commit desired generation and typed invocation", apply, func() error { return store.WriteNode(intent.Node) }); err != nil {
			return err
		}
	}
	if apply {
		if _, _, err := readConsistent(store, current.Node); err != nil {
			return err
		}
	}
	return removeTransactionFile(filepath.Dir(current.RootDisk), "reconcile intent fully committed and verified", report, apply)
}

func desiredDataState(projectValue project.Project, desired spec.Resolved, current []state.DataDisk) ([]state.DataDisk, error) {
	if len(desired.Nodes[0].Disks) == 0 {
		return nil, nil
	}
	if len(desired.Nodes[0].Disks) > 1 {
		return nil, errors.New("quick reconcile supports at most one data disk")
	}
	diskSpec := desired.Nodes[0].Disks[0]
	serial, err := identity.DiskSerial(projectValue.Marker.ProjectID, nodeName, "data")
	if err != nil {
		return nil, err
	}
	nodeDir, err := projectValue.NodeDir(nodeName)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(nodeDir, "data.qcow2")
	if diskSpec.Persistent && len(current) == 1 && current[0].Persistent {
		if current[0].Name != diskSpec.Name || current[0].Serial != serial || current[0].Size != diskSpec.Size || current[0].Mount != diskSpec.Mount {
			return nil, errors.New("retained persistent disk size, mount, or serial is incompatible with reconcile")
		}
		path = current[0].Path
	}
	dataDisk, _ := quickDiskRecords(diskSpec, path, serial)
	if len(current) == 1 && current[0].Name == dataDisk.Name && current[0].Serial == dataDisk.Serial && current[0].Path == dataDisk.Path {
		dataDisk.ActualFilesystem = current[0].ActualFilesystem
	}
	return []state.DataDisk{dataDisk}, nil
}

func (m Manager) stageReconcile(store state.Store, projectState state.ProjectState, node state.NodeState, desired spec.Resolved, action string) (state.Transaction, error) {
	if node.Phase != state.Stopped || process.Alive(node.Process.PID) {
		return state.Transaction{}, errors.New("reconcile requires a stopped node with no live recorded PID")
	}
	operationID, err := m.operationID()
	if err != nil {
		return state.Transaction{}, err
	}
	hash, err := spec.Hash(desired)
	if err != nil {
		return state.Transaction{}, err
	}
	now := time.Now().UTC()
	desiredProject := projectState
	desiredProject.FarrowVersion = m.FarrowVersion
	desiredProject.SpecHash = hash
	desiredProject.Resolved = desired
	desiredProject.UpdatedAt = now
	desiredNode := node
	desiredNode.FarrowVersion = m.FarrowVersion
	desiredNode.Phase = state.Stopped
	desiredNode.Generation++
	desiredNode.SpecHash = hash
	desiredNode.Process = state.ProcessIdentity{}
	desiredNode.Forwards = qemuForwards(desired, node.SSHPort)
	desiredNode.UpdatedAt = now
	desiredNode.DataDisks, err = desiredDataState(store.Project, desired, node.DataDisks)
	if err != nil {
		return state.Transaction{}, err
	}
	desiredNode.Invocation, err = buildInvocation(store.Project, desiredProject, desiredNode)
	if err != nil {
		return state.Transaction{}, err
	}
	seedFiles, err := reconcileSeedFiles(store.Project, desiredProject, desiredNode)
	if err != nil {
		return state.Transaction{}, err
	}
	nodeDir, _ := store.Project.NodeDir(node.Node)
	stagePath := filepath.Join(nodeDir, ".seed.iso.next-"+operationID)
	intent := &state.ReconcileIntent{Action: action, Project: desiredProject, Node: desiredNode, StagedSeed: stagePath}
	transaction := state.Transaction{
		Schema: state.TransactionSchema, FarrowVersion: m.FarrowVersion, OperationID: operationID,
		ProjectID: store.Project.Marker.ProjectID, Node: node.Node, From: state.Stopped, To: state.Prepared,
		Reconcile: intent, StartedAt: now, UpdatedAt: now,
	}
	if err := store.WriteTransaction(transaction); err != nil {
		return state.Transaction{}, err
	}
	if err := cloudinit.BuildISO(stagePath, seedFiles); err != nil {
		_ = removeOwnedRegular(nodeDir, filepath.Join(nodeDir, "transaction.json"))
		return state.Transaction{}, err
	}
	return transaction, nil
}

func (m Manager) beginReconcile(ctx context.Context, store state.Store, projectState state.ProjectState, node state.NodeState, desired spec.Resolved, action string) (state.NodeState, RepairReport, error) {
	transaction, err := m.stageReconcile(store, projectState, node, desired, action)
	if err != nil {
		return state.NodeState{}, RepairReport{}, err
	}
	operationID := transaction.OperationID
	report := RepairReport{ProjectID: store.Project.Marker.ProjectID, OperationID: operationID, Node: node.Node, Apply: true}
	if err := m.repairReconcile(ctx, store, node, transaction, &report, true); err != nil {
		return state.NodeState{}, report, err
	}
	committed, err := store.ReadNode(node.Node)
	return committed, report, err
}

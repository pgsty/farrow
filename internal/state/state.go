// Package state persists the resolved project, node runtime intent, and
// transaction journal as strict versioned JSON.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/network/subnet"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/spec"
)

const (
	ProjectSchema     = 1
	NodeSchema        = 1
	TransactionSchema = 1
)

type Phase string

const (
	Absent     Phase = "absent"
	Preparing  Phase = "preparing"
	Prepared   Phase = "prepared"
	Starting   Phase = "starting"
	Running    Phase = "running"
	Stopping   Phase = "stopping"
	Stopped    Phase = "stopped"
	Destroying Phase = "destroying"
)

func validPhase(phase Phase) bool {
	switch phase {
	case Absent, Preparing, Prepared, Starting, Running, Stopping, Stopped, Destroying:
		return true
	default:
		return false
	}
}

type ProjectState struct {
	Schema        int           `json:"schema"`
	FarrowVersion string        `json:"farrow_version"`
	ProjectID     string        `json:"project_id"`
	SpecHash      string        `json:"spec_hash"`
	Resolved      spec.Resolved `json:"resolved"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type Image struct {
	Alias       string `json:"alias"`
	Release     string `json:"release"`
	Digest      string `json:"digest"`
	VirtualSize int64  `json:"virtual_size"`
}

type DataDisk struct {
	Name                string `json:"name"`
	Path                string `json:"path"`
	Serial              string `json:"serial"`
	Size                int64  `json:"size"`
	Mount               string `json:"mount"`
	Persistent          bool   `json:"persistent"`
	RequestedFilesystem string `json:"requested_filesystem,omitempty"`
	ActualFilesystem    string `json:"actual_filesystem,omitempty"`
}

type RuntimePaths struct {
	Directory string `json:"directory"`
	QMP       string `json:"qmp"`
	PIDFile   string `json:"pidfile"`
}

type ProcessIdentity struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Started    string `json:"started"`
	ArgvHash   string `json:"argv_hash"`
}

type NodeState struct {
	Schema        int             `json:"schema"`
	FarrowVersion string          `json:"farrow_version"`
	ProjectID     string          `json:"project_id"`
	Node          string          `json:"node"`
	VMUUID        string          `json:"vm_uuid"`
	Phase         Phase           `json:"phase"`
	Generation    uint64          `json:"generation"`
	SpecHash      string          `json:"spec_hash"`
	Image         Image           `json:"image"`
	RootDisk      string          `json:"root_disk"`
	DataDisks     []DataDisk      `json:"data_disks,omitempty"`
	Seed          string          `json:"seed"`
	NVRAM         string          `json:"nvram,omitempty"`
	SSHPort       uint16          `json:"ssh_port"`
	Forwards      []qemu.Forward  `json:"forwards"`
	Runtime       RuntimePaths    `json:"runtime"`
	Invocation    qemu.Invocation `json:"invocation"`
	Process       ProcessIdentity `json:"process"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Action struct {
	Name     string `json:"name"`
	Resource string `json:"resource,omitempty"`
}

type ReconcileIntent struct {
	Action     string       `json:"action"`
	Project    ProjectState `json:"project"`
	Node       NodeState    `json:"node"`
	StagedSeed string       `json:"staged_seed"`
}

type Transaction struct {
	Schema        int              `json:"schema"`
	FarrowVersion string           `json:"farrow_version"`
	OperationID   string           `json:"operation_id"`
	ProjectID     string           `json:"project_id"`
	Node          string           `json:"node"`
	From          Phase            `json:"from"`
	To            Phase            `json:"to"`
	Completed     []Action         `json:"completed,omitempty"`
	Reconcile     *ReconcileIntent `json:"reconcile,omitempty"`
	StartedAt     time.Time        `json:"started_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type Store struct {
	Project project.Project
}

func strictRead(path string, destination any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("state must be a regular non-symlink file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("state contains trailing JSON data")
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, append(data, '\n'), 0o600)
}

func (s Store) projectPath() string { return filepath.Join(s.Project.Root, "resolved.json") }

func (s Store) nodePath(name, filename string) (string, error) {
	directory, err := s.Project.NodeDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, filename), nil
}

func validateProject(value ProjectState, expectedID string) error {
	if value.Schema != ProjectSchema {
		return fmt.Errorf("unsupported project state schema %d", value.Schema)
	}
	if value.FarrowVersion == "" || value.ProjectID != expectedID || value.SpecHash == "" || value.UpdatedAt.IsZero() {
		return errors.New("project state version/identity/hash/time is invalid")
	}
	actualHash, err := spec.Hash(value.Resolved)
	if err != nil {
		return err
	}
	if actualHash != value.SpecHash {
		return errors.New("project resolved spec hash mismatch")
	}
	for _, node := range value.Resolved.Nodes {
		for _, forward := range node.Forwards {
			if forward.RequestedHost != 0 && forward.RequestedHost == forward.Host {
				return fmt.Errorf("project resolved forward %s:%d requested_host must differ from its materialized host port", forward.Bind, forward.Host)
			}
		}
	}
	if value.Resolved.Network == "private" {
		if value.Resolved.Private == nil {
			return errors.New("private project state lacks its network contract")
		}
		layout, err := subnet.Parse(value.Resolved.Private.CIDR)
		if err != nil || value.Resolved.Private.HostAddress != layout.HostAddress() || value.Resolved.Private.DHCPEnd != layout.DHCPEnd() {
			return errors.New("private project state network contract is invalid")
		}
		for _, node := range value.Resolved.Nodes {
			if !layout.IsStatic(node.Address) {
				return fmt.Errorf("private project state node %s address is outside the static pool", node.Name)
			}
		}
	}
	return nil
}

func (s Store) WriteProject(value ProjectState) error {
	if err := validateProject(value, s.Project.Marker.ProjectID); err != nil {
		return err
	}
	return writeJSON(s.projectPath(), value)
}

func (s Store) ReadProject() (ProjectState, error) {
	var value ProjectState
	if err := strictRead(s.projectPath(), &value); err != nil {
		return ProjectState{}, err
	}
	if err := validateProject(value, s.Project.Marker.ProjectID); err != nil {
		return ProjectState{}, err
	}
	return value, nil
}

func validateNode(value NodeState, expectedID, expectedNode string) error {
	if value.Schema != NodeSchema {
		return fmt.Errorf("unsupported node state schema %d", value.Schema)
	}
	if value.FarrowVersion == "" || value.ProjectID != expectedID || value.Node != expectedNode || value.VMUUID == "" || !validPhase(value.Phase) || value.Generation == 0 || value.SpecHash == "" || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return errors.New("node state version, identity, phase, generation, hash, or time is invalid")
	}
	for _, disk := range value.DataDisks {
		requested := disk.RequestedFilesystem
		if requested != "" && requested != "auto" && requested != "xfs" && requested != "ext4" {
			return fmt.Errorf("data disk %s has invalid requested filesystem %q", disk.Name, requested)
		}
		actual := disk.ActualFilesystem
		if actual != "" && actual != "xfs" && actual != "ext4" {
			return fmt.Errorf("data disk %s has invalid actual filesystem %q", disk.Name, actual)
		}
	}
	return nil
}

func (s Store) WriteNode(value NodeState) error {
	if err := validateNode(value, s.Project.Marker.ProjectID, value.Node); err != nil {
		return err
	}
	if _, err := s.Project.EnsureNodeDir(value.Node); err != nil {
		return err
	}
	path, err := s.nodePath(value.Node, "state.json")
	if err != nil {
		return err
	}
	return writeJSON(path, value)
}

func (s Store) ReadNode(name string) (NodeState, error) {
	path, err := s.nodePath(name, "state.json")
	if err != nil {
		return NodeState{}, err
	}
	var value NodeState
	if err := strictRead(path, &value); err != nil {
		return NodeState{}, err
	}
	if err := validateNode(value, s.Project.Marker.ProjectID, name); err != nil {
		return NodeState{}, err
	}
	return value, nil
}

func validateTransaction(value Transaction, expectedID string) error {
	if value.Schema != TransactionSchema || value.FarrowVersion == "" || value.ProjectID != expectedID || value.OperationID == "" || value.Node == "" || !validPhase(value.From) || !validPhase(value.To) || value.StartedAt.IsZero() || value.UpdatedAt.IsZero() {
		return errors.New("transaction schema, version, or fields are invalid")
	}
	if value.Reconcile != nil {
		intent := value.Reconcile
		if value.From != Stopped || value.To != Prepared || (intent.Action != "restart" && intent.Action != "stop" && intent.Action != "reconcile") || !filepath.IsAbs(intent.StagedSeed) {
			return errors.New("reconcile transaction transition, action, or staged seed is invalid")
		}
		if err := validateProject(intent.Project, expectedID); err != nil {
			return fmt.Errorf("invalid reconcile project intent: %w", err)
		}
		if err := validateNode(intent.Node, expectedID, value.Node); err != nil {
			return fmt.Errorf("invalid reconcile node intent: %w", err)
		}
		if intent.Node.Phase != Stopped || intent.Node.SpecHash != intent.Project.SpecHash {
			return errors.New("reconcile node must be stopped and match desired project hash")
		}
	}
	return nil
}

func (s Store) WriteTransaction(value Transaction) error {
	if err := validateTransaction(value, s.Project.Marker.ProjectID); err != nil {
		return err
	}
	if _, err := s.Project.EnsureNodeDir(value.Node); err != nil {
		return err
	}
	path, err := s.nodePath(value.Node, "transaction.json")
	if err != nil {
		return err
	}
	return writeJSON(path, value)
}

func (s Store) ReadTransaction(name string) (Transaction, error) {
	path, err := s.nodePath(name, "transaction.json")
	if err != nil {
		return Transaction{}, err
	}
	var value Transaction
	if err := strictRead(path, &value); err != nil {
		return Transaction{}, err
	}
	if err := validateTransaction(value, s.Project.Marker.ProjectID); err != nil {
		return Transaction{}, err
	}
	return value, nil
}

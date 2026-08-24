package quick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/process"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/qmp"
	"github.com/pgsty/piglet/internal/state"
)

type ListedNode struct {
	Name      string      `json:"name"`
	Persisted state.Phase `json:"persisted,omitempty"`
	Actual    string      `json:"actual"`
	Image     string      `json:"image,omitempty"`
	SSHPort   uint16      `json:"ssh_port,omitempty"`
	PID       int         `json:"pid,omitempty"`
	UpdatedAt time.Time   `json:"updated_at,omitempty"`
	Message   string      `json:"message,omitempty"`
}

type ListedProject struct {
	ProjectID string       `json:"project_id"`
	Name      string       `json:"name,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	Root      string       `json:"root"`
	Current   bool         `json:"current"`
	Network   string       `json:"network,omitempty"`
	SpecHash  string       `json:"spec_hash,omitempty"`
	Nodes     []ListedNode `json:"nodes"`
	Integrity string       `json:"integrity,omitempty"`
}

type ListReport struct {
	Schema    int             `json:"schema"`
	DataRoot  string          `json:"data_root"`
	CurrentID string          `json:"current_project_id,omitempty"`
	Projects  []ListedProject `json:"projects"`
	Warnings  []string        `json:"warnings,omitempty"`
}

func missingPath(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var pathError *os.PathError
	return errors.As(err, &pathError) && errors.Is(pathError.Err, os.ErrNotExist)
}

func (m Manager) List(ctx context.Context) (ListReport, error) {
	workDir, err := m.workDir()
	if err != nil {
		return ListReport{}, err
	}
	dataRoot := ""
	currentID := ""
	current, currentErr := project.Open(workDir)
	if currentErr == nil {
		dataRoot = current.DataRoot
		currentID = current.Marker.ProjectID
	} else if !missingPath(currentErr) {
		return ListReport{}, currentErr
	}
	if dataRoot == "" {
		dataRoot, err = project.ResolveDataRoot(workDir, nil)
		if err != nil {
			return ListReport{}, err
		}
	}
	discovery, err := project.Discover(dataRoot)
	if err != nil {
		return ListReport{}, err
	}
	report := ListReport{Schema: 1, DataRoot: discovery.DataRoot, CurrentID: currentID, Warnings: discovery.Warnings}
	for _, projectValue := range discovery.Projects {
		listed := ListedProject{
			ProjectID: projectValue.Marker.ProjectID, CreatedAt: projectValue.Marker.CreatedAt,
			Root: projectValue.Root, Current: projectValue.Marker.ProjectID == currentID,
			Nodes: []ListedNode{},
		}
		store := state.Store{Project: projectValue}
		projectState, projectErr := store.ReadProject()
		if projectErr == nil {
			listed.Name = projectState.Resolved.Name
			listed.Network = projectState.Resolved.Network
			listed.SpecHash = projectState.SpecHash
		} else if !missingPath(projectErr) {
			listed.Integrity = projectErr.Error()
		}
		if projectErr == nil && projectState.Resolved.Network == "private" {
			for _, definition := range projectState.Resolved.Nodes {
				node, nodeErr := store.ReadNode(definition.Name)
				if nodeErr != nil {
					listed.Integrity = strings.TrimSpace(strings.Join([]string{listed.Integrity, fmt.Sprintf("node %s: %v", definition.Name, nodeErr)}, "; "))
					continue
				}
				listedNode := ListedNode{Name: node.Node, Persisted: node.Phase, Actual: "unknown", Image: node.Image.Alias, SSHPort: node.SSHPort, PID: node.Process.PID, UpdatedAt: node.UpdatedAt}
				if node.ProjectID != projectState.ProjectID || node.SpecHash != projectState.SpecHash {
					listed.Integrity = strings.TrimSpace(strings.Join([]string{listed.Integrity, "private node/project identity or spec hash mismatch"}, "; "))
				}
				qmpClient := &qmp.Client{Timeout: 500 * time.Millisecond}
				actualName, nameErr := qmpClient.QueryName(ctx, node.Runtime.QMP)
				actualUUID, uuidErr := qmpClient.QueryUUID(ctx, node.Runtime.QMP)
				identity := process.Identity{PID: node.Process.PID, Executable: node.Process.Executable, Started: node.Process.Started, ArgvHash: node.Process.ArgvHash}
				if nameErr == nil && uuidErr == nil && actualName.Name == node.Node && strings.EqualFold(actualUUID.UUID, node.VMUUID) {
					listedNode.Actual = "running"
					if !process.MatchesLive(ctx, m.runner(), identity, node.Invocation) {
						listedNode.Message = "QMP identity matches; persisted process identity needs private repair"
					}
				} else if process.MatchesLive(ctx, m.runner(), identity, node.Invocation) {
					listedNode.Actual = "degraded"
					listedNode.Message = "process identity matches but QMP identity is unavailable"
				} else {
					listedNode.Actual = "stopped"
					if node.Phase != state.Stopped && node.Phase != state.Prepared {
						listedNode.Message = "persisted phase is stale; run private repair --dry-run"
					}
				}
				listed.Nodes = append(listed.Nodes, listedNode)
			}
			report.Projects = append(report.Projects, listed)
			continue
		}
		node, nodeErr := store.ReadNode(nodeName)
		if missingPath(nodeErr) {
			transaction, transactionErr := store.ReadTransaction(nodeName)
			if transactionErr == nil {
				listed.Nodes = append(listed.Nodes, ListedNode{Name: nodeName, Persisted: transaction.To, Actual: "preparing", Message: "transaction journal exists without stable node state; run repair --dry-run"})
			} else if !missingPath(transactionErr) {
				listed.Integrity = strings.TrimSpace(strings.Join([]string{listed.Integrity, transactionErr.Error()}, "; "))
			}
			report.Projects = append(report.Projects, listed)
			continue
		}
		if nodeErr != nil {
			listed.Integrity = strings.TrimSpace(strings.Join([]string{listed.Integrity, nodeErr.Error()}, "; "))
			report.Projects = append(report.Projects, listed)
			continue
		}
		listedNode := ListedNode{
			Name: node.Node, Persisted: node.Phase, Actual: "unknown", Image: node.Image.Alias,
			SSHPort: node.SSHPort, PID: node.Process.PID, UpdatedAt: node.UpdatedAt,
		}
		if projectErr == nil && node.SpecHash != projectState.SpecHash {
			listed.Integrity = strings.TrimSpace(strings.Join([]string{listed.Integrity, "node/project spec hash mismatch"}, "; "))
		}
		if pathErr := validateNodePaths(projectValue, node); pathErr != nil {
			listed.Integrity = strings.TrimSpace(strings.Join([]string{listed.Integrity, pathErr.Error()}, "; "))
			listedNode.Message = "state paths or artifacts failed integrity validation"
			listed.Nodes = append(listed.Nodes, listedNode)
			report.Projects = append(report.Projects, listed)
			continue
		}
		qmpClient := &qmp.Client{Timeout: 500 * time.Millisecond}
		actualName, nameErr := qmpClient.QueryName(ctx, node.Runtime.QMP)
		actualUUID, uuidErr := qmpClient.QueryUUID(ctx, node.Runtime.QMP)
		if nameErr == nil && uuidErr == nil && actualName.Name == node.Node && strings.EqualFold(actualUUID.UUID, node.VMUUID) {
			listedNode.Actual = "running"
			if !process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
				listedNode.Message = "QMP identity matches; persisted process identity needs repair"
			}
		} else if process.MatchesLive(ctx, m.runner(), processFromState(node.Process), node.Invocation) {
			listedNode.Actual = "degraded"
			listedNode.Message = "process identity matches but QMP identity is unavailable"
		} else {
			listedNode.Actual = "stopped"
			if node.Phase != state.Stopped && node.Phase != state.Prepared {
				listedNode.Message = "persisted phase is stale; run repair --dry-run"
			}
		}
		listed.Nodes = append(listed.Nodes, listedNode)
		report.Projects = append(report.Projects, listed)
	}
	return report, nil
}

func (report ListReport) CurrentRoot() string {
	for _, projectValue := range report.Projects {
		if projectValue.Current {
			return filepath.Clean(projectValue.Root)
		}
	}
	return ""
}

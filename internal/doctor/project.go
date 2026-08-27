package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/state"
	"golang.org/x/sys/unix"
)

func (p Probe) workDir() (string, error) {
	if p.CWD != "" {
		return filepath.Abs(p.CWD)
	}
	return os.Getwd()
}

func availableBytes(pathname string) (uint64, string, error) {
	probe := pathname
	for {
		info, err := os.Lstat(probe)
		if err == nil {
			if !info.IsDir() {
				probe = filepath.Dir(probe)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return 0, "", err
		}
		probe = parent
	}
	var statistics unix.Statfs_t
	if err := unix.Statfs(probe, &statistics); err != nil {
		return 0, "", err
	}
	return uint64(statistics.Bavail) * uint64(statistics.Bsize), probe, nil
}

func (p Probe) projectChecks() []Check {
	workDir, err := p.workDir()
	if err != nil {
		return []Check{{Name: "workspace", Status: Error, Evidence: err.Error()}}
	}
	projectValue, openErr := project.Open(workDir)
	if openErr != nil && !errors.Is(openErr, os.ErrNotExist) {
		return []Check{{Name: "project", Status: Error, Evidence: openErr.Error(), Fix: "repair or restore the matching workspace/data-root project markers"}}
	}
	dataRoot := ""
	checks := make([]Check, 0, 5)
	if openErr == nil {
		dataRoot = projectValue.DataRoot
		checks = append(checks, Check{Name: "project", Status: OK, Evidence: fmt.Sprintf("%s at %s", projectValue.Marker.ProjectID, projectValue.Root)})
		store := state.Store{Project: projectValue}
		projectState, projectErr := store.ReadProject()
		if errors.Is(projectErr, os.ErrNotExist) {
			checks = append(checks, Check{Name: "project-state", Status: Warn, Evidence: "project marker exists but resolved state has not been created", Fix: "run farrow up or farrow validate/plan first"})
		} else if projectErr != nil {
			checks = append(checks, Check{Name: "project-state", Status: Error, Evidence: projectErr.Error(), Fix: "run farrow repair --dry-run; do not hand-edit state"})
		} else {
			checks = append(checks, Check{Name: "project-state", Status: OK, Evidence: fmt.Sprintf("%s network=%s spec=%s", projectState.Resolved.Name, projectState.Resolved.Network, projectState.SpecHash)})
		}
		if projectErr == nil {
			for _, definition := range projectState.Resolved.Nodes {
				node, nodeErr := store.ReadNode(definition.Name)
				checkName := "node-state/" + definition.Name
				if nodeErr == nil {
					checks = append(checks, Check{Name: checkName, Status: OK, Evidence: fmt.Sprintf("phase=%s generation=%d ssh=127.0.0.1:%d", node.Phase, node.Generation, node.SSHPort)})
				} else {
					checks = append(checks, Check{Name: checkName, Status: Error, Evidence: nodeErr.Error(), Fix: "run farrow repair --dry-run " + definition.Name})
					continue
				}
				if projectState.Resolved.Network == "user" {
					if transaction, transactionErr := store.ReadTransaction(definition.Name); transactionErr == nil {
						checks = append(checks, Check{Name: "transaction/" + definition.Name, Status: Warn, Evidence: fmt.Sprintf("pending operation %s: %s -> %s", transaction.OperationID, transaction.From, transaction.To), Fix: "run farrow repair --dry-run before another lifecycle operation"})
					} else if !errors.Is(transactionErr, os.ErrNotExist) {
						checks = append(checks, Check{Name: "transaction/" + definition.Name, Status: Error, Evidence: transactionErr.Error(), Fix: "preserve the journal and inspect with farrow repair --dry-run"})
					}
				} else if nodeDir, pathErr := projectValue.NodeDir(definition.Name); pathErr != nil {
					checks = append(checks, Check{Name: "transaction/" + definition.Name, Status: Error, Evidence: pathErr.Error()})
				} else if info, journalErr := os.Lstat(filepath.Join(nodeDir, "private-prepare.json")); journalErr == nil {
					status := Warn
					evidence := "pending private prepare journal"
					if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
						status, evidence = Error, "private prepare journal is unsafe"
					}
					checks = append(checks, Check{Name: "transaction/" + definition.Name, Status: status, Evidence: evidence, Fix: "run farrow repair --dry-run " + definition.Name})
				} else if !errors.Is(journalErr, os.ErrNotExist) {
					checks = append(checks, Check{Name: "transaction/" + definition.Name, Status: Error, Evidence: journalErr.Error()})
				}
			}
		}
	} else {
		dataRoot, err = project.ResolveDataRoot(workDir, nil)
		if err != nil {
			return append(checks, Check{Name: "data-root", Status: Error, Evidence: err.Error()})
		}
		checks = append(checks, Check{Name: "project", Status: OK, Evidence: "current directory has no Farrow project marker"})
	}
	available, filesystemPath, spaceErr := availableBytes(dataRoot)
	if spaceErr != nil {
		checks = append(checks, Check{Name: "data-root", Status: Error, Evidence: fmt.Sprintf("%s: %v", dataRoot, spaceErr)})
	} else {
		status := OK
		fix := ""
		if available < 20<<30 {
			status = Warn
			fix = "free space or choose a safe absolute FARROW_HOME before creating a project"
		}
		checks = append(checks, Check{Name: "data-root", Status: status, Evidence: fmt.Sprintf("%s (filesystem %s, %.1f GiB available)", dataRoot, filesystemPath, float64(available)/(1<<30)), Fix: fix})
	}
	return checks
}

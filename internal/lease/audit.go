package lease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/process"
	"github.com/pgsty/piglet/internal/qmp"
)

func RuntimeIdentityAuditor(runner execx.Runner, timeout time.Duration) RuntimeAuditor {
	if timeout <= 0 {
		timeout = time.Second
	}
	return func(ctx context.Context, node Node) (Observation, error) {
		if runner == nil {
			return Observation{}, errors.New("runtime identity auditor requires a command runner")
		}
		if node.Runtime.QMP == "" {
			return Observation{Node: node.Name, Authority: "dead", Evidence: "reservation has no runtime paths"}, nil
		}
		qmpExists := false
		if info, err := os.Lstat(node.Runtime.QMP); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
				return Observation{}, fmt.Errorf("node %s QMP path is not an owned Unix socket", node.Name)
			}
			qmpExists = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return Observation{}, err
		}
		if qmpExists {
			client := &qmp.Client{Timeout: timeout}
			actualName, nameErr := client.QueryName(ctx, node.Runtime.QMP)
			if nameErr == nil {
				actualUUID, uuidErr := client.QueryUUID(ctx, node.Runtime.QMP)
				if uuidErr != nil {
					return Observation{}, fmt.Errorf("node %s QMP name responded but UUID failed: %w", node.Name, uuidErr)
				}
				if actualName.Name != node.Name || !strings.EqualFold(actualUUID.UUID, node.VMUUID) {
					return Observation{}, fmt.Errorf("node %s QMP identity mismatch: name=%q uuid=%q", node.Name, actualName.Name, actualUUID.UUID)
				}
				return Observation{Node: node.Name, Live: true, Authority: "qmp", Evidence: "matching QMP name and VM UUID"}, nil
			}
		}
		if completeProcess(node.Process) && process.MatchesLive(ctx, runner, node.Process, node.Invocation) {
			return Observation{Node: node.Name, Live: true, Authority: "process", Evidence: "matching executable, start time, and argv identity"}, nil
		}
		if node.Process.PID > 0 && process.Alive(node.Process.PID) {
			return Observation{}, fmt.Errorf("node %s recorded PID %d is alive but identity does not match", node.Name, node.Process.PID)
		}
		pidEvidence := "pidfile absent"
		if info, err := os.Lstat(node.Runtime.PIDFile); err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 32 {
				return Observation{}, fmt.Errorf("node %s pidfile is unsafe", node.Name)
			}
			data, err := os.ReadFile(node.Runtime.PIDFile)
			if err != nil {
				return Observation{}, err
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil || pid <= 0 {
				return Observation{}, fmt.Errorf("node %s pidfile is malformed", node.Name)
			}
			if process.Alive(pid) {
				return Observation{}, fmt.Errorf("node %s pidfile references unverified live PID %d", node.Name, pid)
			}
			pidEvidence = fmt.Sprintf("pidfile PID %d is dead", pid)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Observation{}, err
		}
		qmpEvidence := "QMP socket absent"
		if qmpExists {
			qmpEvidence = "QMP socket stale/unresponsive"
		}
		return Observation{Node: node.Name, Authority: "dead", Evidence: qmpEvidence + "; " + pidEvidence + "; recorded process identity is dead"}, nil
	}
}

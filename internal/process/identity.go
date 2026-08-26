// Package process captures conservative QEMU process identity. QMP remains the
// primary runtime identity; these facts are the fallback and audit record.
package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/qemu"
)

type Identity struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Started    string `json:"started"`
	ArgvHash   string `json:"argv_hash"`
}

func HashInvocation(invocation qemu.Invocation) (string, error) {
	data, err := json.Marshal(invocation)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func Capture(ctx context.Context, runner execx.Runner, invocation qemu.Invocation, pid int) (Identity, error) {
	if runner == nil || pid <= 0 {
		return Identity{}, errors.New("process runner and positive PID are required")
	}
	psPath, err := exec.LookPath("ps")
	if err != nil {
		return Identity{}, err
	}
	query := func(field string) (string, error) {
		result, runErr := runner.Run(ctx, psPath, "-p", strconv.Itoa(pid), "-o", field+"=")
		if runErr != nil {
			return "", runErr
		}
		return strings.TrimSpace(string(result.Stdout)), nil
	}
	commandLine, err := query("command")
	if err != nil {
		return Identity{}, err
	}
	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return Identity{}, errors.New("process command line is empty")
	}
	executable := fields[0]
	started, err := query("lstart")
	if err != nil {
		return Identity{}, err
	}
	hash := sha256.Sum256([]byte(commandLine))
	identity := Identity{PID: pid, Executable: executable, Started: started, ArgvHash: hex.EncodeToString(hash[:])}
	if filepath.Base(identity.Executable) != filepath.Base(invocation.Binary) {
		return Identity{}, fmt.Errorf("process executable %q does not match QEMU %q", identity.Executable, invocation.Binary)
	}
	return identity, nil
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

func MatchesLive(ctx context.Context, runner execx.Runner, identity Identity, invocation qemu.Invocation) bool {
	if !Alive(identity.PID) || identity.Executable == "" || identity.Started == "" || identity.ArgvHash == "" {
		return false
	}
	current, err := Capture(ctx, runner, invocation, identity.PID)
	return err == nil && current == identity
}

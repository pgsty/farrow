// Package vm runs the shared QEMU/QMP/SSH lifecycle used by product commands.
package vm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/openssh"
	"github.com/pgsty/farrow/internal/process"
	"github.com/pgsty/farrow/internal/qemu"
	"github.com/pgsty/farrow/internal/qmp"
)

type Lifecycle struct {
	Runner  execx.Runner
	QMP     QMPClient
	SSHUser string

	// The hooks below keep process signalling tests deterministic. Production
	// callers leave them nil and use the conservative process package plus the
	// operating system's signal primitive.
	matchesProcess func(context.Context, execx.Runner, process.Identity, qemu.Invocation) bool
	processAlive   func(int) bool
	signalProcess  func(int, syscall.Signal) error
	waitProcess    func(context.Context, int, time.Duration) (bool, error)
	quitTimeout    time.Duration
	termTimeout    time.Duration
	killTimeout    time.Duration
}

// QMPClient is the lifecycle subset of QMP. The concrete qmp.Client satisfies
// it; the narrow interface also lets stop safety be tested without a live VM.
type QMPClient interface {
	QueryName(context.Context, string) (qmp.Name, error)
	QueryUUID(context.Context, string) (qmp.UUID, error)
	Powerdown(context.Context, string) error
	Quit(context.Context, string) error
}

var ErrQMPIdentityMismatch = errors.New("QMP identity mismatch")

const (
	defaultProcessPollInterval = 100 * time.Millisecond
	defaultQMPQuitTimeout      = 10 * time.Second
	defaultSIGTERMTimeout      = 10 * time.Second
	defaultSIGKILLTimeout      = 5 * time.Second
	// GracefulGuestShutdownTimeout covers ordinary systemd shutdown on a
	// provisioned Pigsty node. Services such as Vector may use a 90-second
	// stop timeout; forcing QEMU at the former 60-second boundary risks an
	// unclean database/filesystem shutdown.
	GracefulGuestShutdownTimeout = 2 * time.Minute
)

var sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (l Lifecycle) sshUser() string {
	if l.SSHUser == "" {
		return "dba"
	}
	return l.SSHUser
}

type ReadyMarker struct {
	Project    string `json:"project"`
	Node       string `json:"node"`
	Generation uint64 `json:"generation"`
	SpecHash   string `json:"spec_hash"`
}

func (l Lifecycle) validate() error {
	if l.Runner == nil || l.QMP == nil {
		return errors.New("VM lifecycle requires command runner and QMP client")
	}
	return nil
}

func (l Lifecycle) ValidateIdentity(ctx context.Context, socket, name, uuid string) error {
	if err := l.validate(); err != nil {
		return err
	}
	actualName, err := l.QMP.QueryName(ctx, socket)
	if err != nil {
		return err
	}
	actualUUID, err := l.QMP.QueryUUID(ctx, socket)
	if err != nil {
		return err
	}
	if actualName.Name != name || !strings.EqualFold(actualUUID.UUID, uuid) {
		return fmt.Errorf("%w: name=%q uuid=%q", ErrQMPIdentityMismatch, actualName.Name, actualUUID.UUID)
	}
	return nil
}

func (l Lifecycle) WaitQMP(ctx context.Context, socket, name, uuid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := l.ValidateIdentity(ctx, socket, name, uuid); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("QMP identity did not become ready: %w", lastErr)
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid QEMU pidfile %q", strings.TrimSpace(string(data)))
	}
	return pid, nil
}

func (l Lifecycle) Start(ctx context.Context, invocation qemu.Invocation, socket, pidfile, name, uuid string) (process.Identity, error) {
	return l.start(ctx, invocation, socket, pidfile, name, uuid, nil)
}

func (l Lifecycle) StartWithExtraFiles(ctx context.Context, invocation qemu.Invocation, socket, pidfile, name, uuid string, files []*os.File) (process.Identity, error) {
	if len(files) == 0 {
		return process.Identity{}, errors.New("VM lifecycle extra-file start requires at least one file")
	}
	return l.start(ctx, invocation, socket, pidfile, name, uuid, files)
}

func (l Lifecycle) start(ctx context.Context, invocation qemu.Invocation, socket, pidfile, name, uuid string, files []*os.File) (process.Identity, error) {
	if err := l.validate(); err != nil {
		return process.Identity{}, err
	}
	var runErr error
	if len(files) == 0 {
		_, runErr = l.Runner.Run(ctx, invocation.Binary, invocation.Args...)
	} else if runner, ok := l.Runner.(execx.ExtraFilesRunner); ok {
		_, runErr = runner.RunWithExtraFiles(ctx, invocation.Binary, files, invocation.Args...)
	} else {
		return process.Identity{}, errors.New("VM lifecycle runner cannot pass inherited files")
	}
	if runErr != nil {
		return process.Identity{}, runErr
	}
	if err := l.WaitQMP(ctx, socket, name, uuid, 15*time.Second); err != nil {
		return process.Identity{}, err
	}
	pid, err := readPID(pidfile)
	if err != nil {
		return process.Identity{}, err
	}
	identity, err := process.Capture(ctx, l.Runner, invocation, pid)
	if err != nil {
		return process.Identity{}, err
	}
	if !process.MatchesLive(ctx, l.Runner, identity, invocation) {
		return process.Identity{}, errors.New("started QEMU process identity did not match invocation")
	}
	return identity, nil
}

func (l Lifecycle) Stop(ctx context.Context, socket, name, uuid string, identity process.Identity, invocation qemu.Invocation, guestTimeout time.Duration) error {
	if err := l.validate(); err != nil {
		return err
	}
	if err := l.ValidateIdentity(ctx, socket, name, uuid); err != nil {
		if errors.Is(err, ErrQMPIdentityMismatch) {
			return fmt.Errorf("refuse stop with mismatched QMP identity: %w", err)
		}
		return l.stopWithSignals(ctx, identity, invocation, fmt.Errorf("QMP identity unavailable: %w", err))
	}
	if !l.matchesLive(ctx, identity, invocation) {
		return errors.New("refuse powerdown without matching process identity")
	}
	if err := l.QMP.Powerdown(ctx, socket); err != nil {
		return l.handleQMPFailure(ctx, socket, name, uuid, identity, invocation, "QMP powerdown failed", err)
	}
	exited, err := l.waitForExit(ctx, identity.PID, guestTimeout)
	if err != nil {
		return err
	}
	if exited {
		return nil
	}
	if err := l.ValidateIdentity(ctx, socket, name, uuid); err != nil {
		if errors.Is(err, ErrQMPIdentityMismatch) {
			return fmt.Errorf("guest did not power down; refuse stop with mismatched QMP identity: %w", err)
		}
		return l.stopWithSignals(ctx, identity, invocation, fmt.Errorf("guest did not power down and QMP became unavailable: %w", err))
	}
	if !l.matchesLive(ctx, identity, invocation) {
		return errors.New("guest did not power down; refuse QMP quit without matching process identity")
	}
	if err := l.QMP.Quit(ctx, socket); err != nil {
		return l.handleQMPFailure(ctx, socket, name, uuid, identity, invocation, "QMP quit failed", err)
	}
	exited, err = l.waitForExit(ctx, identity.PID, l.duration(l.quitTimeout, defaultQMPQuitTimeout))
	if err != nil {
		return err
	}
	if exited {
		return nil
	}

	// A successful quit normally closes QMP before the process disappears. Do
	// not assume that happened: prove QMP is unavailable before entering the
	// signal-only fallback. A live or mismatched QMP remains fail-closed.
	if err := l.ValidateIdentity(ctx, socket, name, uuid); err != nil {
		if errors.Is(err, ErrQMPIdentityMismatch) {
			return fmt.Errorf("QEMU remained after QMP quit; refuse stop with mismatched QMP identity: %w", err)
		}
		return l.stopWithSignals(ctx, identity, invocation, fmt.Errorf("QEMU remained after QMP quit and QMP became unavailable: %w", err))
	}
	return fmt.Errorf("QEMU pid %d remained after QMP quit while matching QMP was still available; no signal was sent", identity.PID)
}

func (l Lifecycle) handleQMPFailure(ctx context.Context, socket, name, uuid string, identity process.Identity, invocation qemu.Invocation, operation string, operationErr error) error {
	identityErr := l.ValidateIdentity(ctx, socket, name, uuid)
	if identityErr == nil {
		return fmt.Errorf("%s while matching QMP remained available; no signal was sent: %w", operation, operationErr)
	}
	if errors.Is(identityErr, ErrQMPIdentityMismatch) {
		return fmt.Errorf("%s; refuse stop with mismatched QMP identity: %w", operation, identityErr)
	}
	return l.stopWithSignals(ctx, identity, invocation, fmt.Errorf("%s and QMP became unavailable: %w", operation, operationErr))
}

func (l Lifecycle) stopWithSignals(ctx context.Context, identity process.Identity, invocation qemu.Invocation, reason error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if identity.PID <= 0 || identity.Executable == "" || identity.Started == "" || identity.ArgvHash == "" || invocation.Binary == "" {
		return fmt.Errorf("refuse signal fallback with incomplete process identity or invocation (%v)", reason)
	}
	if !l.alive(identity.PID) {
		return nil
	}
	if !l.matchesLive(ctx, identity, invocation) {
		return fmt.Errorf("refuse signal fallback without matching process identity (%v)", reason)
	}
	if err := l.signal(identity.PID, syscall.SIGTERM); err != nil {
		if !l.alive(identity.PID) {
			return nil
		}
		return fmt.Errorf("send SIGTERM to verified QEMU pid %d: %w", identity.PID, err)
	}
	exited, err := l.waitForExit(ctx, identity.PID, l.duration(l.termTimeout, defaultSIGTERMTimeout))
	if err != nil {
		return err
	}
	if exited {
		return nil
	}

	// Re-capture every identity field immediately before SIGKILL. This is the
	// critical PID-reuse barrier after the bounded SIGTERM wait.
	if err := ctx.Err(); err != nil {
		return err
	}
	if !l.matchesLive(ctx, identity, invocation) {
		return fmt.Errorf("refuse SIGKILL because QEMU process identity changed after SIGTERM (%v)", reason)
	}
	if err := l.signal(identity.PID, syscall.SIGKILL); err != nil {
		if !l.alive(identity.PID) {
			return nil
		}
		return fmt.Errorf("send SIGKILL to verified QEMU pid %d: %w", identity.PID, err)
	}
	exited, err = l.waitForExit(ctx, identity.PID, l.duration(l.killTimeout, defaultSIGKILLTimeout))
	if err != nil {
		return err
	}
	if !exited {
		return fmt.Errorf("verified QEMU pid %d remained after bounded SIGKILL wait", identity.PID)
	}
	return nil
}

func (l Lifecycle) matchesLive(ctx context.Context, identity process.Identity, invocation qemu.Invocation) bool {
	if l.matchesProcess != nil {
		return l.matchesProcess(ctx, l.Runner, identity, invocation)
	}
	return process.MatchesLive(ctx, l.Runner, identity, invocation)
}

func (l Lifecycle) alive(pid int) bool {
	if l.processAlive != nil {
		return l.processAlive(pid)
	}
	return process.Alive(pid)
}

func (l Lifecycle) signal(pid int, signal syscall.Signal) error {
	if l.signalProcess != nil {
		return l.signalProcess(pid, signal)
	}
	target, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return target.Signal(signal)
}

func (l Lifecycle) waitForExit(ctx context.Context, pid int, timeout time.Duration) (bool, error) {
	if l.waitProcess != nil {
		return l.waitProcess(ctx, pid, timeout)
	}
	if !l.alive(pid) {
		return true, nil
	}
	if timeout <= 0 {
		return false, nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(defaultProcessPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
			if !l.alive(pid) {
				return true, nil
			}
		case <-timer.C:
			return !l.alive(pid), nil
		}
	}
}

func (Lifecycle) duration(configured, fallback time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return fallback
}

func SSHArgsForUser(user, key, knownHosts string, port uint16, command ...string) []string {
	if !sshUserPattern.MatchString(user) {
		return nil
	}
	knownHostsOption, err := openssh.QuoteConfigValue(knownHosts)
	if err != nil {
		return nil
	}
	args := []string{
		"-F", "/dev/null", "-i", key,
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + knownHostsOption,
		"-o", "ConnectTimeout=5", "-p", strconv.Itoa(int(port)), user + "@127.0.0.1",
	}
	if len(command) > 0 {
		args = append(args, QuoteRemote(command))
	}
	return args
}

// QuoteRemote preserves argv boundaries across OpenSSH's string-valued exec
// request. Each argument is single-quoted and embedded quotes use the standard
// close/escaped-quote/reopen sequence.
func QuoteRemote(command []string) string {
	quoted := make([]string, 0, len(command))
	for _, argument := range command {
		quoted = append(quoted, "'"+strings.ReplaceAll(argument, "'", "'\"'\"'")+"'")
	}
	return strings.Join(quoted, " ")
}

func (l Lifecycle) WaitReady(ctx context.Context, sshPath, key, knownHosts string, port uint16, expected ReadyMarker, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		args := SSHArgsForUser(l.sshUser(), key, knownHosts, port, "cat", "/var/lib/farrow/ready.json")
		if args == nil {
			return fmt.Errorf("invalid SSH user %q", l.sshUser())
		}
		result, err := l.Runner.Run(ctx, sshPath, args...)
		if err == nil {
			var marker ReadyMarker
			if decodeErr := json.Unmarshal(result.Stdout, &marker); decodeErr == nil && marker == expected {
				return nil
			} else if decodeErr != nil {
				lastErr = decodeErr
			} else {
				lastErr = fmt.Errorf("ready marker mismatch: %#v", marker)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("SSH readiness timeout: %w", lastErr)
}

// Package execx runs bounded external commands without invoking a shell.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const defaultOutputLimit = 1 << 20

// Result is the bounded output and process metadata from one invocation.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
}

// Runner is implemented by OSRunner and by deterministic test fakes.
type Runner interface {
	Run(ctx context.Context, binary string, args ...string) (Result, error)
}

// ExtraFilesRunner is the narrow extension used by the Darwin private-network
// FD fallback. File index zero is inherited by the child as descriptor 3.
type ExtraFilesRunner interface {
	RunWithExtraFiles(ctx context.Context, binary string, files []*os.File, args ...string) (Result, error)
}

// OSRunner executes argv directly. Timeout zero means use only the caller's
// context. OutputLimit zero selects a conservative default.
type OSRunner struct {
	Timeout     time.Duration
	OutputLimit int
}

// CommandError preserves controlled stderr and the exit code without exposing
// an unbounded command output.
type CommandError struct {
	Binary   string
	Args     []string
	ExitCode int
	Stderr   string
	Cause    error
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s failed with exit code %d: %v", Display(e.Binary, e.Args...), e.ExitCode, e.Cause)
	}
	return fmt.Sprintf("%s failed with exit code %d: %s", Display(e.Binary, e.Args...), e.ExitCode, e.Stderr)
}

func (e *CommandError) Unwrap() error { return e.Cause }

// Run executes binary with args as an argv slice. It never invokes a shell.
func (r OSRunner) Run(ctx context.Context, binary string, args ...string) (Result, error) {
	return r.run(ctx, binary, nil, args...)
}

func (r OSRunner) RunWithExtraFiles(ctx context.Context, binary string, files []*os.File, args ...string) (Result, error) {
	if len(files) == 0 || len(files) > 16 {
		return Result{}, errors.New("external command extra-file count must be 1..16")
	}
	for _, file := range files {
		if file == nil {
			return Result{}, errors.New("external command extra file is nil")
		}
	}
	return r.run(ctx, binary, files, args...)
}

func (r OSRunner) run(ctx context.Context, binary string, files []*os.File, args ...string) (Result, error) {
	if binary == "" {
		return Result{}, errors.New("external command binary is empty")
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	stdout := newLimitedBuffer(limit)
	stderr := newLimitedBuffer(limit)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.ExtraFiles = files
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := time.Now()
	err := cmd.Run()
	result := Result{
		Stdout:   bytes.Clone(stdout.Bytes()),
		Stderr:   bytes.Clone(stderr.Bytes()),
		ExitCode: 0,
		Duration: time.Since(started),
	}
	if err == nil {
		return result, nil
	}

	result.ExitCode = -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	return result, &CommandError{
		Binary:   binary,
		Args:     append([]string(nil), args...),
		ExitCode: result.ExitCode,
		Stderr:   strings.TrimSpace(string(result.Stderr)),
		Cause:    err,
	}
}

// Display returns a human-readable representation only. The returned string
// must never be fed to a shell for execution.
func Display(binary string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strconv.Quote(binary))
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

type limitedBuffer struct {
	buf       bytes.Buffer
	remaining int
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{remaining: limit} }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if b.remaining > 0 {
		keep := len(p)
		if keep > b.remaining {
			keep = b.remaining
		}
		_, _ = b.buf.Write(p[:keep])
		b.remaining -= keep
	}
	return original, nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

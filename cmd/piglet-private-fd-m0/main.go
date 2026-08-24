// piglet-private-fd-m0 exercises the product private controller while forcing
// the composed Go-dial + ExtraFiles descriptor-3 fallback on Darwin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/config"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/lease"
	privatevm "github.com/pgsty/piglet/internal/private"
	"github.com/pgsty/piglet/internal/project"
	"github.com/pgsty/piglet/internal/state"
	"github.com/pgsty/piglet/internal/version"
	"github.com/pgsty/piglet/internal/vm"
)

type evidence struct {
	Schema      int               `json:"schema"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at"`
	ProjectID   string            `json:"project_id,omitempty"`
	SpecHash    string            `json:"spec_hash,omitempty"`
	FDNodes     []string          `json:"fd_nodes,omitempty"`
	HostTCP     map[string]string `json:"host_tcp,omitempty"`
	GuestChecks map[string]string `json:"guest_checks,omitempty"`
	FirstStart  privatevm.Status  `json:"first_start"`
	Restart     privatevm.Status  `json:"restart"`
	FinalStop   privatevm.Status  `json:"final_stop"`
	LeaseAbsent bool              `json:"lease_absent"`
	Result      string            `json:"result"`
	Error       string            `json:"error,omitempty"`
}

func main() {
	flags := flag.NewFlagSet("piglet-private-fd-m0", flag.ExitOnError)
	configPath := flags.String("f", "", "absolute private Piglet YAML")
	workdir := flags.String("work-dir", "", "new or existing project working directory")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall timeout")
	flags.Parse(os.Args[1:])
	if flags.NArg() != 0 || !filepath.IsAbs(*configPath) || !filepath.IsAbs(*workdir) {
		fmt.Fprintln(os.Stderr, "usage: piglet-private-fd-m0 -f <absolute-private-yaml> --work-dir <absolute-directory>")
		os.Exit(2)
	}
	record := evidence{Schema: 1, StartedAt: time.Now().UTC(), HostTCP: make(map[string]string), GuestChecks: make(map[string]string), Result: "failed"}
	err := run(*configPath, *workdir, *timeout, &record)
	record.FinishedAt = time.Now().UTC()
	if err != nil {
		record.Error = err.Error()
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(record)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(configPath, workdir string, timeout time.Duration, record *evidence) (returnErr error) {
	file, err := config.Load(configPath)
	if err != nil {
		return err
	}
	resolved, err := file.Resolve()
	if err != nil || resolved.Network != "private" {
		return errors.New("FD M0 requires a valid private configuration")
	}
	manager := privatevm.Manager{CWD: workdir, PigletVersion: version.Version, ForceDarwinFD: true, ReadyTimeout: 3 * time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	started := false
	defer func() {
		if started && returnErr != nil {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cleanupCancel()
			_, _ = manager.Stop(cleanupContext)
		}
	}()
	first, err := manager.Up(ctx, resolved)
	if err != nil {
		return err
	}
	started = true
	record.FirstStart = first
	projectValue, err := project.Open(workdir)
	if err != nil {
		return err
	}
	record.ProjectID = projectValue.Marker.ProjectID
	projectState, err := (state.Store{Project: projectValue}).ReadProject()
	if err != nil {
		return err
	}
	record.SpecHash = projectState.SpecHash
	store := state.Store{Project: projectValue}
	for _, definition := range projectState.Resolved.Nodes {
		node, err := store.ReadNode(definition.Name)
		if err != nil || !node.Invocation.UsesPrivateFD3() {
			return fmt.Errorf("node %s did not persist the descriptor-3 backend", definition.Name)
		}
		record.FDNodes = append(record.FDNodes, definition.Name)
		connection, err := net.DialTimeout("tcp", net.JoinHostPort(definition.Address, "22"), 5*time.Second)
		if err != nil {
			return fmt.Errorf("host TCP to %s: %w", definition.Name, err)
		}
		_ = connection.Close()
		record.HostTCP[definition.Name] = definition.Address + ":22"
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	runner := execx.OSRunner{Timeout: 30 * time.Second, OutputLimit: 1 << 20}
	for index, definition := range projectState.Resolved.Nodes {
		peer := projectState.Resolved.Nodes[(index+1)%len(projectState.Resolved.Nodes)].Address
		connection, err := manager.Connection(ctx, definition.Name)
		if err != nil {
			return err
		}
		args := vm.SSHArgsForUser(connection.User, connection.PrivateKey, connection.KnownHosts, connection.Port, "bash", "-lc", "ip -4 route get "+peer+"; /usr/local/libexec/piglet-network-check")
		result, err := runner.Run(ctx, sshPath, args...)
		if err != nil || !strings.Contains(string(result.Stdout), "private0") || !strings.Contains(string(result.Stdout), "HTTP/") {
			return fmt.Errorf("guest FD network check %s failed: %w: %s", definition.Name, err, result.Stdout)
		}
		record.GuestChecks[definition.Name] = strings.TrimSpace(string(result.Stdout))
	}
	if _, err := manager.Stop(ctx); err != nil {
		return err
	}
	started = false
	restarted, err := manager.Start(ctx)
	if err != nil {
		return err
	}
	started = true
	record.Restart = restarted
	final, err := manager.Stop(ctx)
	if err != nil {
		return err
	}
	started = false
	record.FinalStop = final
	leaseStatus, err := (lease.Store{}).Inspect()
	if err != nil {
		return err
	}
	record.LeaseAbsent = !leaseStatus.Active
	if !record.LeaseAbsent {
		return errors.New("private lease remains after final FD stop")
	}
	record.Result = "passed"
	return nil
}

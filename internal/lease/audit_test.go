package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/execx"
)

type rejectingRunner struct{}

func (rejectingRunner) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{}, errors.New("runner should not be called")
}

func serveQMPIdentity(t *testing.T, socket, name, uuid string) {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for connectionIndex := 0; connectionIndex < 2; connectionIndex++ {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			decoder := json.NewDecoder(connection)
			encoder := json.NewEncoder(connection)
			_ = encoder.Encode(map[string]any{"QMP": map[string]any{"version": map[string]any{}, "capabilities": []any{}}})
			for requestIndex := 0; requestIndex < 2; requestIndex++ {
				var request map[string]any
				if decoder.Decode(&request) != nil {
					break
				}
				execute, _ := request["execute"].(string)
				id, _ := request["id"].(string)
				result := any(map[string]any{})
				if execute == "query-name" {
					result = map[string]any{"name": name}
				} else if execute == "query-uuid" {
					result = map[string]any{"UUID": uuid}
				}
				_ = encoder.Encode(map[string]any{"return": result, "id": id})
			}
			_ = connection.Close()
		}
	}()
}

func TestRuntimeIdentityAuditorQMPFirstAuthority(t *testing.T) {
	t.Parallel()
	directory, err := os.MkdirTemp("/tmp", "farrow-lease-qmp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(directory, "qmp.sock"))
		_ = os.Remove(directory)
	})
	node := newLease(t, 10).Nodes[0]
	node = preparedNode(node)
	node.Runtime.Directory = directory
	node.Runtime.QMP = filepath.Join(directory, "qmp.sock")
	node.Runtime.PIDFile = filepath.Join(directory, "qemu.pid")
	serveQMPIdentity(t, node.Runtime.QMP, node.Name, node.VMUUID)
	observation, err := RuntimeIdentityAuditor(rejectingRunner{}, time.Second)(context.Background(), node)
	if err != nil || !observation.Live || observation.Authority != "qmp" {
		t.Fatalf("QMP observation = %#v, %v", observation, err)
	}
}

func TestRuntimeIdentityAuditorDeadAndUnverifiedPID(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	node := newLease(t, 10).Nodes[0]
	node = preparedNode(node)
	node.Runtime.Directory = directory
	node.Runtime.QMP = filepath.Join(directory, "qmp.sock")
	node.Runtime.PIDFile = filepath.Join(directory, "qemu.pid")
	if err := os.WriteFile(node.Runtime.PIDFile, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditor := RuntimeIdentityAuditor(rejectingRunner{}, 100*time.Millisecond)
	observation, err := auditor(context.Background(), node)
	if err != nil || observation.Live || observation.Authority != "dead" {
		t.Fatalf("dead observation = %#v, %v", observation, err)
	}
	if err := os.WriteFile(node.Runtime.PIDFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := auditor(context.Background(), node); err == nil {
		t.Fatal("unverified live pidfile was considered dead")
	}
}

func TestRuntimeIdentityAuditorReservedNodeIsDeadAfterGrace(t *testing.T) {
	t.Parallel()
	node := newLease(t, 10).Nodes[0]
	observation, err := RuntimeIdentityAuditor(rejectingRunner{}, time.Second)(context.Background(), node)
	if err != nil || observation.Live || observation.Authority != "dead" {
		t.Fatalf("reserved observation = %#v, %v", observation, err)
	}
}

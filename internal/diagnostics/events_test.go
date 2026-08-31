package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAppendEventConcurrentAtomic(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "node")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	pathname := filepath.Join(directory, "events.jsonl")
	const count = 32
	var wait sync.WaitGroup
	errorsFound := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			event := Event{
				Schema: 1, Time: time.Unix(int64(index+1), 0).UTC(), Level: "info",
				Node: "meta", OperationID: fmt.Sprintf("operation-%08d", index),
				Action: "test", Phase: "running", Message: "node event message",
			}
			if err := AppendEvent(context.Background(), pathname, event); err != nil {
				errorsFound <- err
			}
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	info, err := os.Lstat(pathname)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("event log mode = %v, %v", info, err)
	}
	handle, err := os.Open(pathname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("close event log: %v", err)
		}
	})
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(handle)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("torn event line: %v: %q", err, scanner.Bytes())
		}
		if event.Message != "node event message" {
			t.Fatalf("event message was altered: %#v", event)
		}
		seen[event.OperationID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != count {
		t.Fatalf("event count = %d, want %d", len(seen), count)
	}
}

func TestAppendQEMULogBoundsIdentity(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "node")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	pathname := filepath.Join(directory, "qemu.log")
	record := QEMULogRecord{
		Schema: 1, Time: time.Now().UTC(), Level: "error",
		Node: "meta", OperationID: "operation-12345678", Action: "launch",
		Message: "qemu terminated unexpectedly",
	}
	if err := AppendQEMULog(context.Background(), pathname, record); err != nil {
		t.Fatal(err)
	}
	invalid := record
	invalid.Node = "Not Valid!"
	if err := AppendQEMULog(context.Background(), pathname, invalid); err == nil {
		t.Fatal("invalid node identity was accepted")
	}
	data, err := os.ReadFile(pathname)
	if err != nil {
		t.Fatal(err)
	}
	var decoded QEMULogRecord
	if err := json.Unmarshal(bytes.TrimSpace(data), &decoded); err != nil || decoded.Action != "launch" {
		t.Fatalf("QEMU log JSON = %#v, %v", decoded, err)
	}
}

func TestDiagnosticLogRetentionKeepsCompleteNewestRecords(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "node")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	pathname := filepath.Join(directory, "qemu.log")
	record := QEMULogRecord{Schema: 1, Time: time.Unix(1, 0).UTC(), Level: "info", Node: "meta", OperationID: "operation-12345678", Action: "launch", Message: "bounded record"}
	encoded, _ := json.Marshal(record)
	line := append(encoded, '\n')
	content := bytes.Repeat(line, int(maxDiagnosticLogBytes/int64(len(line)))+2)
	if err := os.WriteFile(pathname, content, 0o600); err != nil {
		t.Fatal(err)
	}
	record.Action = "retained"
	if err := AppendQEMULog(context.Background(), pathname, record); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(pathname)
	if err != nil || info.Size() > retainedDiagnosticLogBytes+maxEventBytes {
		t.Fatalf("retained log size = %v, %v", info, err)
	}
	handle, err := os.Open(pathname)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("close retained QEMU log: %v", err)
		}
	})
	scanner := bufio.NewScanner(handle)
	lastAction := ""
	count := 0
	for scanner.Scan() {
		var decoded QEMULogRecord
		if err := json.Unmarshal(scanner.Bytes(), &decoded); err != nil {
			t.Fatalf("retention produced torn JSON: %v", err)
		}
		lastAction = decoded.Action
		count++
	}
	if err := scanner.Err(); err != nil || count == 0 || lastAction != "retained" {
		t.Fatalf("retained records count=%d last=%q err=%v", count, lastAction, err)
	}
}

func TestAppendEventRejectsSymlink(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "node")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	pathname := filepath.Join(directory, "events.jsonl")
	if err := os.Symlink(target, pathname); err != nil {
		t.Fatal(err)
	}
	event := Event{Schema: 1, Time: time.Now().UTC(), Level: "info", Node: "meta", OperationID: "operation-12345678", Action: "test"}
	if err := AppendEvent(context.Background(), pathname, event); err == nil {
		t.Fatal("symlink event target was accepted")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "preserve" {
		t.Fatalf("external event target changed: %q %v", data, err)
	}
}

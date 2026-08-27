package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/project"
)

func TestCobraRootAndContextualHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("root help code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"Project Setup:", "Lifecycle:", "Guest Access:", "ss", "Use \"farrow [command] --help\""} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("root help missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("root help wrote stderr: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"status", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("status help code=%d stderr=%s", code, stderr.String())
	}
	for _, irrelevant := range []string{"--cpus", "--data-disk", "--delete-persistent", "--repo"} {
		if strings.Contains(stdout.String(), irrelevant) {
			t.Errorf("status help exposes irrelevant flag %s:\n%s", irrelevant, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "Global Flags:") {
		t.Fatalf("status help lacks global flags:\n%s", stdout.String())
	}
}

func TestSSHShortcutUsesProjectDirectoryName(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "dev")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(work, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	arguments, err := sshShortcutArguments("", []string{"meta"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--install", "--name", "dev", "meta"}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("shortcut arguments=%v want=%v", arguments, want)
	}
}

func TestSSHShortcutAcceptsExplicitName(t *testing.T) {
	arguments, err := sshShortcutArguments("dev", []string{"meta", "node-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--install", "--name", "dev", "meta", "node-1"}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("shortcut arguments=%v want=%v", arguments, want)
	}
}

func TestViperOutputEnvironment(t *testing.T) {
	t.Setenv("FARROW_OUTPUT", "json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("version code=%d stderr=%s", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["name"] != "farrow" {
		t.Fatalf("output=%q result=%v err=%v", stdout.String(), result, err)
	}
}

func TestCompletionRejectsUnsupportedShell(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"completion", "nonsense"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("completion code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unsupported completion shell") {
		t.Fatalf("completion stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStructuredUsageErrorIsParseableAndDiagnostic(t *testing.T) {
	for _, arguments := range [][]string{{"--json", "image"}, {"--json", "up", "--bogus"}} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(arguments, &stdout, &stderr); code != exitUsage {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			var failure commandFailure
			if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
				t.Fatalf("structured usage output is not JSON: %v\n%s", err, stdout.String())
			}
			if failure.Error != "usage" || failure.Message == "" || !strings.HasPrefix(stderr.String(), "error: ") {
				t.Fatalf("failure=%#v stderr=%q", failure, stderr.String())
			}
		})
	}
}

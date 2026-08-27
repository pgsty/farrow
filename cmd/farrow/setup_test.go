package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/execx"
	netpreflight "github.com/pgsty/farrow/internal/network/preflight"
	setuphost "github.com/pgsty/farrow/internal/setup"
)

type setupSequenceRunner struct {
	calls  int
	failAt int
}

func (runner *setupSequenceRunner) Run(context.Context, string, ...string) (execx.Result, error) {
	runner.calls++
	if runner.calls == runner.failAt {
		return execx.Result{ExitCode: 42}, errors.New("fixture failure")
	}
	return execx.Result{}, nil
}

func TestResolveSetupSelectionDefaultsToQuick(t *testing.T) {
	t.Parallel()
	selection, err := resolveSetupSelection("", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if selection.Mode != "quick" || selection.Profile != "quick" || selection.Publish || selection.ConfigPath != "" {
		t.Fatalf("quick selection = %#v", selection)
	}
}

func TestResolveSetupSelectionGeneratesPrivateProfileOnce(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	selection, err := resolveSetupSelection("meta", "", "", directory)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Mode != "private" || selection.Profile != "meta" || !selection.Publish || !selection.Generated || len(selection.ConfigData) == 0 {
		t.Fatalf("generated selection = %#v", selection)
	}
	if selection.ConfigPath != filepath.Join(directory, "farrow.yaml") {
		t.Fatalf("config path = %s", selection.ConfigPath)
	}
	if err := os.WriteFile(selection.ConfigPath, selection.ConfigData, 0o600); err != nil {
		t.Fatal(err)
	}
	repeated, err := resolveSetupSelection("meta", "", "", directory)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Publish || repeated.Mode != "private" {
		t.Fatalf("repeated selection = %#v", repeated)
	}
}

func TestResolveSetupSelectionPreservesDifferentExistingConfig(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	meta, err := resolveSetupSelection("meta", "", "", directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta.ConfigPath, meta.ConfigData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSetupSelection("full", "", "", directory); err == nil {
		t.Fatal("setup accepted a profile over a different existing farrow.yaml")
	}
	data, err := os.ReadFile(meta.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, meta.ConfigData) {
		t.Fatal("existing farrow.yaml changed")
	}
}

func TestGeneratedSetupProfileCanRebaseBeforePublish(t *testing.T) {
	t.Parallel()
	selection, err := resolveSetupSelection("full", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := selection.rebaseGenerated("172.31.251.0/24"); err != nil {
		t.Fatal(err)
	}
	if selection.Resolved.Private == nil || selection.Resolved.Private.CIDR != "172.31.251.0/24" || !selection.Publish {
		t.Fatalf("rebased selection = %#v", selection)
	}
}

func TestSetupFileCannotBeSilentlyRebased(t *testing.T) {
	t.Parallel()
	if _, err := resolveSetupSelection("", "/tmp/farrow.yaml", "172.31.251.0/24", t.TempDir()); err == nil {
		t.Fatal("-f accepted --network-cidr")
	}
}

func TestConstrainSetupNetworkModeReusesDefaultButRejectsExplicitMismatch(t *testing.T) {
	t.Parallel()
	report := netpreflight.Report{
		OS: "darwin", Ready: true,
		Installation: netpreflight.Installation{Status: "exact", Mode: "shared", Healthy: true},
	}
	defaulted, effective := constrainSetupNetworkMode(report, "host", false)
	if !defaulted.Ready || effective != "shared" {
		t.Fatalf("default mode = ready:%v effective:%q", defaulted.Ready, effective)
	}
	explicit, effective := constrainSetupNetworkMode(report, "host", true)
	if explicit.Ready || explicit.ExitCode != exitConflict || effective != "host" || len(explicit.Findings) != 1 {
		t.Fatalf("explicit mode = %#v effective=%q", explicit, effective)
	}
}

func TestSetupApplyCommandRemovesDryRunAndPreservesTypedArguments(t *testing.T) {
	t.Parallel()
	context := &outputContext{format: outputYAML, verbose: true}
	stdout := &outputWriter{Writer: &bytes.Buffer{}, context: context}
	stderr := &outputWriter{Writer: &bytes.Buffer{}, context: context, stderr: true}
	next, argv := setupApplyCommand([]string{"full", "--mode=shared", "--dry-run=1", "--network-cidr", "172.31.251.0/24"}, stdout, stderr)
	want := []string{"farrow", "setup", "full", "--mode=shared", "--network-cidr", "172.31.251.0/24", "--yaml", "--verbose"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") || strings.Contains(next, "dry-run") {
		t.Fatalf("next=%q argv=%#v", next, argv)
	}
}

func TestSetupNextCommandUsesShellQuoteAndTypedArgv(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "lab $(touch nope).yaml")
	selection := setupSelection{ConfigPath: path}
	next, argv := setupNextCommand(selection, directory)
	if len(argv) != 4 || argv[3] != path || !strings.Contains(next, "'"+path+"'") {
		t.Fatalf("next=%q argv=%#v", next, argv)
	}
}

func TestEmitPrivatePendingNetworkDoesNotClaimUserNAT(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := setupResult{
		Schema: 1, OS: "darwin", Arch: "arm64", Mode: "private", Profile: "full",
		DryRun: true, NetworkCIDR: "10.10.10.0/24", NetworkMode: "host", Next: "farrow up",
	}
	if code := emitSetupResult(result, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "user NAT") || !strings.Contains(stdout.String(), "pending dependencies") {
		t.Fatalf("output=%q", stdout.String())
	}
}

func TestRunSetupEmitsStructuredEarlyFailure(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	context := &outputContext{format: outputJSON}
	out := &outputWriter{Writer: &stdout, context: context}
	errOut := &outputWriter{Writer: &stderr, context: context, stderr: true}
	code := runSetupCommand("profile-that-does-not-exist", setupCLIOptions{Mode: "host"}, out, errOut)
	if code != exitUsage {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	var result setupResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode structured failure: %v; stdout=%q", err, stdout.String())
	}
	if result.Schema != 1 || result.Ready || result.ExitCode != exitUsage || result.Error == "" {
		t.Fatalf("structured failure = %#v", result)
	}
}

func TestRunSetupHelpIsSuccessful(t *testing.T) {
	for _, test := range []struct {
		name   string
		format string
	}{
		{name: "text"},
		{name: "json", format: "--json"},
		{name: "yaml", format: "--yaml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{"setup", "--help"}
			if test.format != "" {
				arguments = append(arguments, test.format)
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(arguments, &stdout, &stderr); code != exitOK {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("help wrote diagnostics to stderr: %q", stderr.String())
			}
			help := stdout.String()
			if !strings.Contains(help, "Usage:") || !strings.Contains(help, "farrow setup") || strings.Contains(help, "flag: help requested") {
				t.Fatalf("unexpected setup help:\n%s", help)
			}
		})
	}
}

func TestRunSetupTreatsSingleDashModeAsExplicit(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"setup", "quick", "-mode=host", "--dry-run", "--json"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var failure commandFailure
	if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil || failure.Error != "usage" || !strings.Contains(failure.Message, "unknown shorthand flag") || !strings.Contains(stderr.String(), "unknown shorthand flag") {
		t.Fatalf("non-standard single-dash long flag was accepted: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestSetupCommandFailureSeparatesChangedFromUncertain(t *testing.T) {
	commands := []setuphost.Command{
		{Name: "first", Binary: "/usr/bin/true"},
		{Name: "second", Binary: "/usr/bin/false"},
	}
	for _, test := range []struct {
		name        string
		failAt      int
		wantChanged bool
	}{
		{name: "first command", failAt: 1, wantChanged: false},
		{name: "after one success", failAt: 2, wantChanged: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &setupSequenceRunner{failAt: test.failAt}
			changed, uncertain, err := runSetupCommands(context.Background(), commands, runner, &bytes.Buffer{})
			if err == nil || changed != test.wantChanged || !uncertain {
				t.Fatalf("changed=%t uncertain=%t err=%v", changed, uncertain, err)
			}
		})
	}
}

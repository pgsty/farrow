package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgsty/farrow/internal/image"
	darwinnet "github.com/pgsty/farrow/internal/network/darwin"
	"github.com/pgsty/farrow/internal/spec"
	"github.com/pgsty/farrow/internal/sshconfig"
	"github.com/pgsty/farrow/internal/sshkeys"
	"github.com/pgsty/farrow/internal/state"
)

type recordingDarwinInstaller struct {
	mode  string
	cidr  string
	apply bool
}

func (r *recordingDarwinInstaller) InstallModeNetwork(_ context.Context, _, _, _, mode, cidr string, apply bool) (darwinnet.InstallReport, error) {
	r.mode, r.cidr, r.apply = mode, cidr, apply
	return darwinnet.InstallReport{Action: "install", Applied: apply}, nil
}

func TestDarwinInstallForwardsSharedMode(t *testing.T) {
	recorder := &recordingDarwinInstaller{}
	report, err := installDarwinNetwork(context.Background(), recorder, "/archive", "uuid", "arm64", "shared", "172.31.251.0/24", true)
	if err != nil || recorder.mode != "shared" || recorder.cidr != "172.31.251.0/24" || !recorder.apply || !report.Applied {
		t.Fatalf("mode=%q cidr=%q apply=%t report=%#v err=%v", recorder.mode, recorder.cidr, recorder.apply, report, err)
	}
}

func TestSortedMapKeysMakesPlanOutputDeterministic(t *testing.T) {
	got := strings.Join(sortedMapKeys(map[string]string{"/z": "z", "/a": "a", "/m": "m"}), ",")
	if got != "/a,/m,/z" {
		t.Fatalf("sorted keys = %q", got)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "farrow dev") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestInitInvalidNetworkCIDRIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "full", "--network-cidr", "not-a-cidr"}, &stdout, &stderr)
	if code != exitUsage || !strings.Contains(stderr.String(), "canonical IPv4 /24") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestConfigurationWarningsIncludeNonLoopbackForwards(t *testing.T) {
	t.Parallel()
	resolved := spec.Quick(true, false)
	resolved.Nodes[0].Forwards = []spec.Forward{
		{Bind: "127.0.0.1", Host: 15432, Guest: 5432, Protocol: "tcp"},
		{Bind: "0.0.0.0", Host: 13000, Guest: 3000, Protocol: "tcp"},
	}
	warnings := configurationWarnings(resolved)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "exposes host TCP 0.0.0.0:13000 beyond loopback") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestTestingImageWarningIsProminent(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	printImageStatusWarning(&output, image.Entry{Alias: "u24", Arch: "arm64", Release: "test", Status: "testing"})
	if !strings.Contains(output.String(), "warning: image u24/arm64") || !strings.Contains(output.String(), "status testing, not supported") {
		t.Fatalf("warning = %q", output.String())
	}
	output.Reset()
	printImageStatusWarning(&output, image.Entry{Alias: "u24", Status: "supported"})
	if output.Len() != 0 {
		t.Fatalf("supported image warning = %q", output.String())
	}
}

func TestDestructiveConfirmationTTYAndNonTTY(t *testing.T) {
	t.Parallel()
	var prompt bytes.Buffer
	if err := confirmDestructive(false, true, "destroy", strings.NewReader("destroy\n"), &prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.String(), `typing "destroy"`) {
		t.Fatalf("prompt = %q", prompt.String())
	}
	if err := confirmDestructive(false, true, "destroy", strings.NewReader("no\n"), io.Discard); err == nil {
		t.Fatal("mismatched interactive confirmation accepted")
	}
	if err := confirmDestructive(false, false, "destroy", strings.NewReader("destroy\n"), io.Discard); err == nil || !strings.Contains(err.Error(), "requires --force") {
		t.Fatalf("non-TTY confirmation error = %v", err)
	}
	if err := confirmDestructive(true, false, "destroy", strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("--force rejected: %v", err)
	}
}

func TestRollbackFlagIsUpOnly(t *testing.T) {
	t.Setenv("FARROW_HOME", t.TempDir())
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"plan", "--rollback"}, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "unknown flag") {
		t.Fatalf("plan code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"up", "--rollback"}, &stdout, &stderr); code != exitConflict || !strings.Contains(stderr.String(), "no configuration found") {
		t.Fatalf("configless up code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestProvisionRejectsUnsafeInputBeforeProjectAccess(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "setup.sh")
	if err := os.WriteFile(scriptPath, []byte("echo ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(root, "setup-link.sh")
	if err := os.Symlink(scriptPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		options provisionOptions
		code    int
		want    string
	}{
		{name: "missing script flag", options: provisionOptions{Parallelism: 1, Timeout: time.Hour}, code: exitUsage, want: "--script is required"},
		{name: "missing script file", options: provisionOptions{ScriptPath: filepath.Join(root, "missing.sh"), Parallelism: 1, Timeout: time.Hour}, code: exitUsage, want: "inspect provision script"},
		{name: "invalid parallelism", options: provisionOptions{ScriptPath: scriptPath, Parallelism: 0, Timeout: time.Hour}, code: exitUsage, want: "parallelism must be"},
		{name: "invalid timeout", options: provisionOptions{ScriptPath: scriptPath, Parallelism: 1, Timeout: 25 * time.Hour}, code: exitUsage, want: "no more than 24h"},
		{name: "symlink script", options: provisionOptions{ScriptPath: symlinkPath, Parallelism: 1, Timeout: time.Hour}, code: exitIntegrity, want: "non-symlink"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runProvision(test.options, nil, &stdout, &stderr); code != test.code || !strings.Contains(stderr.String(), test.want) || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestProvisionConnectionExitClassifiesSSHArtifactIntegrity(t *testing.T) {
	if code := provisionConnectionExit(&sshkeys.SSHArtifactError{Err: errors.New("unsafe fixture")}); code != exitIntegrity {
		t.Fatalf("SSH artifact error exit = %d", code)
	}
	if code := provisionConnectionExit(errors.New("runtime identity mismatch")); code != exitRuntime {
		t.Fatalf("runtime error exit = %d", code)
	}
}

func TestCommandTimeoutHonorsResolvedReadiness(t *testing.T) {
	t.Parallel()
	resolved := spec.Quick(true, true)
	if got := withReadinessTimeout(10*time.Minute, resolved); got != 10*time.Minute {
		t.Fatalf("default command timeout = %s", got)
	}
	resolved.SSHWaitTimeoutNS = int64(20 * time.Minute)
	if got := withReadinessTimeout(10*time.Minute, resolved); got != 25*time.Minute {
		t.Fatalf("extended command timeout = %s", got)
	}
	resolved.SSHWaitTimeoutNS = -1
	if got := withReadinessTimeout(10*time.Minute, resolved); got != 10*time.Minute {
		t.Fatalf("invalid timeout changed command deadline: %s", got)
	}
}

func TestLifecycleSequenceKeepsReloadDistinctFromRestart(t *testing.T) {
	if got := strings.Join(lifecycleSequence("reload"), ","); got != "stop,up" {
		t.Fatalf("reload sequence = %q", got)
	}
	if got := strings.Join(lifecycleSequence("restart"), ","); got != "restart" {
		t.Fatalf("restart sequence = %q", got)
	}
}

func TestLoadPrivatePreflightConfigAcceptsRelativePath(t *testing.T) {
	resolved, err := loadPrivatePreflightConfig("../../tests/fixtures/private-two.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Network != "private" || resolved.Private.CIDR != "10.10.10.0/24" || len(resolved.Nodes) != 2 {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func TestImageListJSON(t *testing.T) {
	t.Setenv("FARROW_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := run([]string{"image", "list", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"alias": "u24"`, `"arch": "arm64"`, `"status": "testing"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("image JSON missing %q: %s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), `"alias": "el8"`) {
		t.Fatalf("retired EL8 alias remained in the embedded image list: %s", stdout.String())
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
		t.Fatalf("empty invocation code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, args := range [][]string{{"unknown"}, {"version", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("run(%v) code=%d, want %d", args, code, exitUsage)
		}
	}
}

func TestDeletePersistentRequiresDestroyAndForce(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"up", "--delete-persistent"},
		{"recreate", "--force", "--delete-persistent"},
		{"destroy", "--delete-persistent"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("run(%v) code=%d stderr=%s, want usage", args, code, stderr.String())
		}
	}
}

func TestNoWaitOnlyAppliesToStartingCommands(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"plan", "--no-wait"},
		{"status", "--no-wait"},
		{"stop", "--no-wait"},
		{"destroy", "--force", "--no-wait"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("run(%v) code=%d stderr=%s, want usage", args, code, stderr.String())
		}
	}
}

func TestCompletions(t *testing.T) {
	t.Setenv("FARROW_HOME", t.TempDir())
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"completion", shell}, &stdout, &stderr); code != exitOK {
			t.Fatalf("%s completion code=%d stderr=%s", shell, code, stderr.String())
		}
		for _, want := range []string{"farrow", "__farrow"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("%s completion missing %q: %s", shell, want, stdout.String())
			}
		}
	}
	for _, test := range []struct {
		args []string
		want []string
	}{
		{args: []string{"__complete", "setup", ""}, want: []string{"meta", "dual", "trio", "full"}},
		{args: []string{"__complete", "setup", "--mode", ""}, want: []string{"host", "shared"}},
		{args: []string{"__complete", "image", ""}, want: []string{"list", "pull", "sync"}},
		{args: []string{"__complete", "image", "pull", "u"}, want: []string{"u24"}},
		{args: []string{"__complete", "up", "--file", "../../tests/fixtures/private-two.yaml", ""}, want: []string{"meta", "node-1"}},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(test.args, &stdout, &stderr); code != exitOK {
			t.Fatalf("dynamic completion %v code=%d stderr=%s", test.args, code, stderr.String())
		}
		for _, want := range test.want {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("dynamic completion %v missing %q: %s", test.args, want, stdout.String())
			}
		}
	}
}

func TestDeploymentDestroyUsesPersistedStateWithoutConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	resolved := spec.Resolved{
		Schema: 1, Name: "private", Image: "u24", Network: "private", SSHUser: "dba",
		Private: &spec.PrivateNetwork{CIDR: "10.10.10.0/24", HostAddress: "10.10.10.1", DHCPEnd: "10.10.10.8"},
		Nodes:   []spec.Node{{Name: "meta", Control: true, Address: "10.10.10.10", CPUs: 1, Memory: 2 * spec.GiB, RootDisk: 8 * spec.GiB}},
	}
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := (state.Store{Root: root}).WriteDeployment(state.DeploymentState{Schema: state.DeploymentSchema, FarrowVersion: "test", SpecHash: hash, Resolved: resolved, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"destroy"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "destroy requires --force") {
		t.Fatalf("deployment destroy dispatch was ambiguous: %s", stderr.String())
	}
}

func TestLegacyDeploymentDiagnosticsDistinguishExistingState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", root)
	resolved := spec.Quick(true, false)
	hash, err := spec.Hash(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := (state.Store{Root: root}).WriteDeployment(state.DeploymentState{
		Schema: state.DeploymentSchema, FarrowVersion: "test", SpecHash: hash,
		Resolved: resolved, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"logs", "ssh-config", "status", "ssh"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run([]string{"--json", command}, &stdout, &stderr); code != exitConflict {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", command, code, stdout.String(), stderr.String())
		}
		var failure commandFailure
		if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
			t.Fatalf("%s failure JSON: %v\n%s", command, err, stdout.String())
		}
		if failure.Error != "conflict" || failure.Message != legacyDeploymentMessage || !strings.Contains(stderr.String(), "predates the fixed-IP redesign") {
			t.Fatalf("%s failure=%#v stderr=%q", command, failure, stderr.String())
		}
	}
}

func TestMissingDeploymentDiagnosticsRemainDistinctFromLegacyState(t *testing.T) {
	t.Setenv("FARROW_HOME", t.TempDir())
	const missingMessage = "no deployment state found; run `farrow up` first"
	for _, command := range []string{"logs", "ssh-config", "status", "ssh"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run([]string{"--json", command}, &stdout, &stderr); code != exitConflict {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", command, code, stdout.String(), stderr.String())
		}
		var failure commandFailure
		if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
			t.Fatalf("%s failure JSON: %v\n%s", command, err, stdout.String())
		}
		if failure.Error != "conflict" || failure.Message != missingMessage || strings.Contains(failure.Message, legacyDeploymentMessage) {
			t.Fatalf("%s failure=%#v stderr=%q", command, failure, stderr.String())
		}
	}
}

func TestSSHConfigRemoveRemainsAvailableAfterResolvedStateIsGone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FARROW_HOME", filepath.Join(root, "data"))
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := sshconfig.Entry{
		Name: "lab", Node: "meta", User: "dba",
		Host: "127.0.0.1", Port: 2222, Identity: filepath.Join(root, "key"), KnownHosts: filepath.Join(root, "known"),
	}
	installed, err := sshconfig.Install(home, entry)
	if err != nil {
		t.Fatal(err)
	}
	// Destroy deliberately preserves keys and integration rollback authority;
	// removal must work with no deployment state present at all.
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ssh-config", "--remove", "--name", "lab", "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("remove code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Lstat(installed.Fragment); !os.IsNotExist(err) {
		t.Fatalf("owned fragment remains after state-independent remove: %v", err)
	}
}

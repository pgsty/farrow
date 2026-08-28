package execx

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDisplayKeepsArgumentsSeparate(t *testing.T) {
	t.Parallel()
	got := Display("qemu-img", "create", "a b", "$(touch nope)")
	if !strings.Contains(got, `"a b"`) || !strings.Contains(got, `"$(touch nope)"`) {
		t.Fatalf("display command did not quote arguments: %s", got)
	}
}

func TestOSRunnerBoundsOutput(t *testing.T) {
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Skip("printf executable unavailable")
	}
	runner := OSRunner{Timeout: 5 * time.Second, OutputLimit: 32}
	result, err := runner.Run(context.Background(), printf, "%s", strings.Repeat("x", 4096))
	if err != nil {
		t.Fatalf("run printf: %v", err)
	}
	if len(result.Stdout) != 32 {
		t.Fatalf("stdout length = %d, want 32", len(result.Stdout))
	}
}

func TestOSRunnerInheritsProxyEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9443")
	printenv, err := exec.LookPath("printenv")
	if err != nil {
		t.Skip("printenv executable unavailable")
	}
	result, err := (OSRunner{Timeout: 5 * time.Second}).Run(context.Background(), printenv, "HTTPS_PROXY")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(result.Stdout)); got != "http://127.0.0.1:9443" {
		t.Fatalf("inherited HTTPS_PROXY = %q", got)
	}
}

func TestOSRunnerPassesExtraFileAsFD3(t *testing.T) {
	t.Parallel()
	catPath, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("cat executable unavailable")
	}
	file, err := os.CreateTemp(t.TempDir(), "fd-input-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("inherited-fd-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	result, err := (OSRunner{Timeout: 5 * time.Second}).RunWithExtraFiles(context.Background(), catPath, []*os.File{file}, "/dev/fd/3")
	if err != nil || string(result.Stdout) != "inherited-fd-3" {
		t.Fatalf("extra file result=%q err=%v", result.Stdout, err)
	}
}

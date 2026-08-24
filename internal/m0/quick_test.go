package m0

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pgsty/piglet/internal/spec"
)

func TestRandomUUIDContract(t *testing.T) {
	t.Parallel()
	first, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(first) || first == second {
		t.Fatalf("UUID contract failure: %q %q", first, second)
	}
}

func TestChoosePortsSkipsOccupiedPreferredPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:15432")
	if err != nil {
		t.Skipf("preferred test port already occupied: %v", err)
	}
	defer listener.Close()
	quick := spec.Quick(true, true)
	_, forwards, err := choosePorts(quick.Nodes[0].Forwards)
	if err != nil {
		t.Fatal(err)
	}
	for _, forward := range forwards {
		if forward.Guest == 5432 {
			if forward.Host != 25432 {
				t.Fatalf("occupied 15432 resolved to %d, want 25432", forward.Host)
			}
			return
		}
	}
	t.Fatal("PostgreSQL forward missing")
}

func TestPrepareWorkDirRejectsBroadOrNonEmptyTargets(t *testing.T) {
	t.Parallel()
	if err := prepareWorkDir("relative"); err == nil {
		t.Fatal("relative work directory unexpectedly accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "owned"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareWorkDir(dir); err == nil {
		t.Fatal("non-empty work directory unexpectedly accepted")
	}
}

func TestQuickSmokeRejectsDigestMismatchBeforeQEMU(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	image := filepath.Join(root, "base.qcow2")
	if err := os.WriteFile(image, []byte("not-a-real-image"), 0o400); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "work")
	_, err := QuickSmoke(context.Background(), QuickOptions{Image: image, ExpectedSHA: strings.Repeat("0", 64), WorkDir: work})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("digest mismatch error = %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(work, "evidence.json"))
	if readErr != nil {
		t.Fatalf("failure evidence missing: %v", readErr)
	}
	var evidence QuickEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.Events) == 0 || evidence.Events[len(evidence.Events)-1].Result != "failed" {
		t.Fatalf("failure event missing: %#v", evidence.Events)
	}
}

func TestSSHArgsDoNotLoadUserConfigOrDisableHostKeys(t *testing.T) {
	t.Parallel()
	args := strings.Join(sshArgs("/key", "/known", 2222, "true"), " ")
	for _, want := range []string{"-F /dev/null", "BatchMode=yes", "IdentitiesOnly=yes", "StrictHostKeyChecking=accept-new", `UserKnownHostsFile="/known"`, "dba@127.0.0.1 true"} {
		if !strings.Contains(args, want) {
			t.Errorf("SSH args missing %q: %s", want, args)
		}
	}
	if strings.Contains(args, "StrictHostKeyChecking=no") {
		t.Fatal("SSH host-key checking disabled")
	}
}

func TestParseDFSize(t *testing.T) {
	t.Parallel()
	got, err := parseDFSize("  1B-blocks\n  66571993088\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != 66571993088 {
		t.Fatalf("size = %d", got)
	}
	if _, err := parseDFSize("garbage"); err == nil {
		t.Fatal("invalid df output unexpectedly accepted")
	}
}

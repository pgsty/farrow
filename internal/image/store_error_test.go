package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCachedDigestMismatchHasFactualError(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	pathname := filepath.Join(dataRoot, "u24", "fixture.qcow2")
	if err := os.MkdirAll(filepath.Dir(pathname), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathname, []byte("corrupt"), 0o444); err != nil {
		t.Fatal(err)
	}
	entry := Entry{File: "u24/fixture.qcow2", SHA256: strings.Repeat("0", 64), Format: "qcow2", ArtifactSize: int64(len("corrupt"))}
	_, _, err := (Store{DataRoot: dataRoot}).ValidateCached(context.Background(), entry)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") || strings.Contains(err.Error(), "%!w") || strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

package portregistry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pgsty/farrow/internal/project"
)

func TestReservedFailsClosedOnCorruptRegisteredState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := project.Create(work, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	nodeDir, err := projectValue.EnsureNodeDir("meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "state.json"), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reserved(projectValue.DataRoot); err == nil {
		t.Fatal("corrupt registered state was ignored during port allocation")
	}
}

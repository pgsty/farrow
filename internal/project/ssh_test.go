package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func sshArtifactFixture(t *testing.T) (Project, string) {
	t.Helper()
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectValue, err := Create(workDir, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	keysDir := filepath.Join(projectValue.Root, "keys")
	if err := os.Mkdir(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id_ed25519", "known_hosts"} {
		if err := os.WriteFile(filepath.Join(keysDir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return projectValue, keysDir
}

func TestValidateSSHArtifactsReturnsExactPaths(t *testing.T) {
	t.Parallel()
	projectValue, keysDir := sshArtifactFixture(t)
	privateKey, knownHosts, err := ValidateSSHArtifacts(projectValue)
	if err != nil {
		t.Fatal(err)
	}
	if privateKey != filepath.Join(keysDir, "id_ed25519") || knownHosts != filepath.Join(keysDir, "known_hosts") {
		t.Fatalf("SSH artifacts = %q, %q", privateKey, knownHosts)
	}
}

func TestValidateSSHArtifactsRejectsUnsafeLinksAndModes(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "symlink",
			mutate: func(t *testing.T, keysDir string) {
				outside := filepath.Join(filepath.Dir(keysDir), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(keysDir, "known_hosts")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hardlink",
			mutate: func(t *testing.T, keysDir string) {
				if err := os.Link(filepath.Join(keysDir, "known_hosts"), filepath.Join(filepath.Dir(keysDir), "outside-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mode",
			mutate: func(t *testing.T, keysDir string) {
				if err := os.Chmod(filepath.Join(keysDir, "id_ed25519"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "truncated",
			mutate: func(t *testing.T, keysDir string) {
				if err := os.Truncate(filepath.Join(keysDir, "known_hosts"), 0); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			projectValue, keysDir := sshArtifactFixture(t)
			test.mutate(t, keysDir)
			if _, _, err := ValidateSSHArtifacts(projectValue); err == nil {
				t.Fatal("unsafe SSH artifact was accepted")
			} else {
				var integrityError *SSHArtifactError
				if !errors.As(err, &integrityError) {
					t.Fatalf("unsafe SSH artifact error = %T, want *SSHArtifactError", err)
				}
			}
		})
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogSignGenerateSignVerifyAndRejectTamper(t *testing.T) {
	t.Setenv("CATALOGSIGN_PASSWORD", "test-password")
	directory := t.TempDir()
	if err := run([]string{"generate", directory, "fixture"}); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(directory, "fixture.key")
	publicKey := filepath.Join(directory, "fixture.pub")
	catalog := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalog, []byte("catalog fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", key, catalog}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", publicKey, catalog}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog, []byte("tampered catalog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", publicKey, catalog}); err == nil {
		t.Fatal("catalog signature accepted tampered bytes")
	}
	if err := run([]string{"generate", directory, "fixture"}); err == nil {
		t.Fatal("key generation overwrote an existing keypair")
	}
	if err := run([]string{"unknown"}); err == nil {
		t.Fatal("catalogsign accepted an unknown subcommand")
	}
}

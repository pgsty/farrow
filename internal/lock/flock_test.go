package lock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestExclusiveLockHonorsContext(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "project.lock")
	first, err := Acquire(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, path, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExclusiveRejectsWrongReleasedAndSharedTokens(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "project.lock")
	exclusive, err := Acquire(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := exclusive.ValidateExclusive(path); err != nil {
		t.Fatalf("live exclusive token rejected: %v", err)
	}
	if err := exclusive.ValidateExclusive(filepath.Join(root, ".", "other.lock")); err == nil {
		t.Fatal("exclusive token accepted a different path")
	}
	if err := exclusive.Release(); err != nil {
		t.Fatal(err)
	}
	if err := exclusive.ValidateExclusive(path); err == nil {
		t.Fatal("released exclusive token was accepted")
	}

	shared, err := Acquire(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Release()
	if err := shared.ValidateExclusive(path); err == nil {
		t.Fatal("shared token was accepted as exclusive")
	}
}

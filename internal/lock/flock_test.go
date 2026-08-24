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

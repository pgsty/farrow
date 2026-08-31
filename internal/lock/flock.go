// Package lock implements bounded advisory file locks for Farrow's fixed lock
// order: cache/global, private lease, project, then node.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type File struct {
	mu     sync.Mutex
	handle *os.File
	path   string
	shared bool
}

func Acquire(ctx context.Context, path string, shared bool) (*File, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("lock path must be absolute")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refuse symlink lock path: %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	handle, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	operation := unix.LOCK_EX | unix.LOCK_NB
	if shared {
		operation = unix.LOCK_SH | unix.LOCK_NB
	}
	for {
		if err := unix.Flock(int(handle.Fd()), operation); err == nil {
			return &File{handle: handle, path: path, shared: shared}, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = handle.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = handle.Close()
			return nil, fmt.Errorf("lock %s: %w", path, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (f *File) Path() string { return f.path }

// ValidateExclusive proves that the token still owns an unreleased exclusive
// lock for exactly path. Locked helpers use this instead of relying on a
// naming convention that callers could accidentally violate.
func (f *File) ValidateExclusive(path string) error {
	if f == nil {
		return errors.New("exclusive lock token is nil")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handle == nil {
		return errors.New("exclusive lock token has been released")
	}
	if f.shared {
		return errors.New("lock token is shared, not exclusive")
	}
	if path == "" || f.path != path {
		return fmt.Errorf("lock token path mismatch: held %q, required %q", f.path, path)
	}
	if _, err := f.handle.Stat(); err != nil {
		return fmt.Errorf("exclusive lock token is not live: %w", err)
	}
	return nil
}

func (f *File) Release() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handle == nil {
		return nil
	}
	unlockErr := unix.Flock(int(f.handle.Fd()), unix.LOCK_UN)
	closeErr := f.handle.Close()
	f.handle = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// JoinRelease preserves the operation error while making a failed unlock or
// close visible to the caller. Lock release is part of every state mutation's
// durability boundary, not best-effort cleanup.
func JoinRelease(current error, held *File, description string) error {
	if err := held.Release(); err != nil {
		return errors.Join(current, fmt.Errorf("release %s: %w", description, err))
	}
	return current
}

// Package lock implements bounded advisory file locks for Piglet's fixed lock
// order: cache/global, private lease, project, then node.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type File struct {
	handle *os.File
	path   string
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
			return &File{handle: handle, path: path}, nil
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

func (f *File) Release() error {
	if f == nil || f.handle == nil {
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

//go:build darwin

package hostconfig

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func createMetadataTemp(source, parent, pattern string) (*os.File, string, error) {
	placeholder, err := os.CreateTemp(parent, pattern)
	if err != nil {
		return nil, "", err
	}
	path := placeholder.Name()
	if err := placeholder.Close(); err != nil {
		_ = os.Remove(path)
		return nil, "", err
	}
	if err := os.Remove(path); err != nil {
		return nil, "", err
	}
	if err := unix.Clonefile(source, path, unix.CLONE_NOFOLLOW); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		_ = os.Remove(path)
		return nil, "", err
	}
	return file, path, nil
}

func platformFileFlags(_ string, info os.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("darwin file has no stat flags")
	}
	return uint64(stat.Flags), nil
}

func verifyPlatformFileFlags(path string, _ os.FileInfo, expected uint64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	actual, err := platformFileFlags(path, info)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("darwin clone did not preserve file flags")
	}
	return nil
}

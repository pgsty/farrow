//go:build linux

package hostconfig

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func createMetadataTemp(_ string, parent, pattern string) (*os.File, string, error) {
	file, err := os.CreateTemp(parent, pattern)
	if err != nil {
		return nil, "", err
	}
	return file, file.Name(), nil
}

func platformFileFlags(path string, _ os.FileInfo) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	flags, err := unix.IoctlGetInt(int(file.Fd()), unix.FS_IOC_GETFLAGS)
	if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EOPNOTSUPP) {
		return 0, nil
	}
	return uint64(flags), err
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
		return errors.New("linux replacement would change inode flags")
	}
	return nil
}

package hostconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const maxMetadataBytes = 1 << 20

type fileMetadata struct {
	xattrs map[string][]byte
	flags  uint64
}

func listXattrNames(path string) ([]string, error) {
	size, err := unix.Listxattr(path, nil)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	if size < 0 || size > maxMetadataBytes {
		return nil, errors.New("hosts extended-attribute name list exceeds limit")
	}
	buffer := make([]byte, size)
	written, err := unix.Listxattr(path, buffer)
	if err != nil {
		return nil, err
	}
	buffer = buffer[:written]
	names := make([]string, 0)
	for _, name := range strings.Split(strings.TrimRight(string(buffer), "\x00"), "\x00") {
		if name == "" || strings.ContainsRune(name, 0) {
			return nil, errors.New("hosts extended-attribute name is invalid")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func readXattrs(path string) (map[string][]byte, error) {
	names, err := listXattrNames(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(names))
	total := 0
	for _, name := range names {
		size, err := unix.Getxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		if size < 0 || total+size > maxMetadataBytes {
			return nil, errors.New("hosts extended-attribute values exceed limit")
		}
		value := make([]byte, size)
		written, err := unix.Getxattr(path, name, value)
		if err != nil {
			return nil, err
		}
		result[name] = append([]byte(nil), value[:written]...)
		total += written
	}
	return result, nil
}

func captureFileMetadata(path string, info os.FileInfo) (fileMetadata, error) {
	xattrs, err := readXattrs(path)
	if err != nil {
		return fileMetadata{}, fmt.Errorf("read hosts extended metadata: %w", err)
	}
	flags, err := platformFileFlags(path, info)
	if err != nil {
		return fileMetadata{}, fmt.Errorf("read hosts file flags: %w", err)
	}
	return fileMetadata{xattrs: xattrs, flags: flags}, nil
}

func syncXattrs(path string, desired map[string][]byte) error {
	current, err := readXattrs(path)
	if err != nil {
		return err
	}
	for name := range current {
		if _, keep := desired[name]; !keep {
			if err := unix.Removexattr(path, name); err != nil {
				return err
			}
		}
	}
	for name, value := range desired {
		if actual, exists := current[name]; exists && bytes.Equal(actual, value) {
			continue
		}
		if err := unix.Setxattr(path, name, value, 0); err != nil {
			return err
		}
	}
	return nil
}

func applyFileMetadata(path string, info os.FileInfo, desired fileMetadata) error {
	if err := syncXattrs(path, desired.xattrs); err != nil {
		return fmt.Errorf("copy hosts extended metadata: %w", err)
	}
	if err := verifyPlatformFileFlags(path, info, desired.flags); err != nil {
		return fmt.Errorf("preserve hosts file flags: %w", err)
	}
	return verifyFileMetadata(path, desired)
}

func verifyFileMetadata(path string, expected fileMetadata) error {
	actual, err := readXattrs(path)
	if err != nil {
		return err
	}
	if len(actual) != len(expected.xattrs) {
		return errors.New("hosts extended-attribute set changed")
	}
	for name, value := range expected.xattrs {
		if !bytes.Equal(actual[name], value) {
			return fmt.Errorf("hosts extended attribute %q changed", name)
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	flags, err := platformFileFlags(path, info)
	if err != nil {
		return err
	}
	if flags != expected.flags {
		return errors.New("hosts file flags changed")
	}
	return nil
}

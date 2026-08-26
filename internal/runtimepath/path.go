// Package runtimepath owns short, user-scoped QMP and pidfile directories.
package runtimepath

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"syscall"

	"github.com/pgsty/farrow/internal/project"
)

var (
	shortProjectPattern  = regexp.MustCompile(`^[0-9a-f]{8}$`)
	shortNodePattern     = regexp.MustCompile(`^[0-9a-f]{10}$`)
	legacyQuickPattern   = regexp.MustCompile(`^farrow-([0-9]+)-([0-9a-f]{8})-meta$`)
	legacyPrivatePattern = regexp.MustCompile(`^pl-([0-9]+)-([0-9a-f]{8})-([0-9a-f]{10})$`)
)

func fallbackBase(uid int) string {
	root := "/tmp"
	if runtime.GOOS == "darwin" {
		root = "/private/tmp"
	}
	return filepath.Join(root, fmt.Sprintf("farrow-runtime-%d", uid))
}

func ownerUID(info os.FileInfo) (int, bool) {
	value, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(value.Uid), true
}

func validateOwnedDirectory(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := ownerUID(info)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !ok || owner != uid {
		return fmt.Errorf("runtime parent must be a real owner-%d mode-0700 directory: %s", uid, path)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return fmt.Errorf("runtime parent must have a canonical non-symlink path: %s", path)
	}
	return nil
}

func selectedBase(uid int) (string, error) {
	if uid < 0 {
		return "", errors.New("runtime UID is invalid")
	}
	if configured := os.Getenv("XDG_RUNTIME_DIR"); configured != "" {
		if !filepath.IsAbs(configured) || filepath.Clean(configured) != configured {
			return "", errors.New("XDG_RUNTIME_DIR must be a clean absolute path")
		}
		if err := validateOwnedDirectory(configured, uid); err != nil {
			return "", err
		}
		return configured, nil
	}
	return fallbackBase(uid), nil
}

func nodeToken(projectID, node string) string {
	digest := sha256.Sum256([]byte(projectID + "\x00" + node))
	return hex.EncodeToString(digest[:5])
}

// Directory returns a short new-runtime location without creating it.
func Directory(projectID, node string, uid int) (string, error) {
	if !project.ValidUUID(projectID) || node == "" {
		return "", errors.New("runtime project or node identity is invalid")
	}
	base, err := selectedBase(uid)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(base, "farrow", projectID[:8], nodeToken(projectID, node))
	if err := Validate(directory, projectID, node, uid); err != nil {
		return "", err
	}
	return directory, nil
}

func maxSocketPath() int {
	if runtime.GOOS == "darwin" {
		return 103
	}
	return 107
}

// Validate proves a persisted new-runtime path belongs to the project/node.
// The base may be absent after cleanup; Ensure performs ownership checks.
func Validate(directory, projectID, node string, uid int) error {
	if !project.ValidUUID(projectID) || node == "" || uid < 0 || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("runtime directory identity is invalid")
	}
	nodeDir := filepath.Base(directory)
	projectDir := filepath.Base(filepath.Dir(directory))
	farrowDir := filepath.Dir(filepath.Dir(directory))
	base := filepath.Dir(farrowDir)
	if filepath.Base(farrowDir) != "farrow" || projectDir != projectID[:8] || nodeDir != nodeToken(projectID, node) || !shortProjectPattern.MatchString(projectDir) || !shortNodePattern.MatchString(nodeDir) || base == "/" || base == "." {
		return errors.New("runtime directory does not match project/node identity")
	}
	if len(filepath.Join(directory, "qmp.sock")) > maxSocketPath() {
		return errors.New("runtime QMP socket path exceeds the platform limit")
	}
	return nil
}

func legacyUID(directory string) (int, bool) {
	if filepath.Dir(directory) != "/tmp" && filepath.Dir(directory) != "/private/tmp" {
		return 0, false
	}
	for _, pattern := range []*regexp.Regexp{legacyQuickPattern, legacyPrivatePattern} {
		match := pattern.FindStringSubmatch(filepath.Base(directory))
		if len(match) == 0 {
			continue
		}
		uid, err := strconv.Atoi(match[1])
		return uid, err == nil
	}
	return 0, false
}

func ensureOne(path string, uid int, allowCreate bool) error {
	if err := validateOwnedDirectory(path, uid); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) || !allowCreate {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateOwnedDirectory(path, uid)
}

func ensureLegacy(path string, uid int) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := ownerUID(info)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !ok || owner != uid {
		return fmt.Errorf("legacy runtime directory must be a real owner-%d mode-0700 directory: %s", uid, path)
	}
	return nil
}

// Ensure creates only the validated, user-owned runtime chain. Exact legacy
// flat /tmp paths are accepted so existing pre-v1 state remains startable.
func Ensure(directory string, uid int) error {
	if legacy, ok := legacyUID(directory); ok {
		if legacy != uid {
			return errors.New("legacy runtime UID differs from the invoking user")
		}
		return ensureLegacy(directory, uid)
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("runtime directory must be a clean absolute path")
	}
	nodeDir := directory
	projectDir := filepath.Dir(nodeDir)
	farrowDir := filepath.Dir(projectDir)
	base := filepath.Dir(farrowDir)
	if filepath.Base(farrowDir) != "farrow" || !shortProjectPattern.MatchString(filepath.Base(projectDir)) || !shortNodePattern.MatchString(filepath.Base(nodeDir)) {
		return errors.New("runtime directory has an invalid managed layout")
	}
	allowBaseCreate := base == fallbackBase(uid)
	if err := ensureOne(base, uid, allowBaseCreate); err != nil {
		return err
	}
	for _, path := range []string{farrowDir, projectDir, nodeDir} {
		if err := ensureOne(path, uid, true); err != nil {
			return err
		}
	}
	return nil
}

// PruneEmptyParents removes only empty managed parents after the node runtime
// directory has gone. It never removes XDG_RUNTIME_DIR itself.
func PruneEmptyParents(directory string, uid int) error {
	if _, legacy := legacyUID(directory); legacy {
		return nil
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("runtime directory must be a clean absolute path")
	}
	projectDir := filepath.Dir(directory)
	farrowDir := filepath.Dir(projectDir)
	base := filepath.Dir(farrowDir)
	if filepath.Base(farrowDir) != "farrow" || !shortProjectPattern.MatchString(filepath.Base(projectDir)) || !shortNodePattern.MatchString(filepath.Base(directory)) {
		return errors.New("runtime directory has an invalid managed layout")
	}
	candidates := []string{projectDir, farrowDir}
	if base == fallbackBase(uid) {
		candidates = append(candidates, base)
	}
	for _, path := range candidates {
		if err := validateOwnedDirectory(path, uid); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.Remove(path); errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			return nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

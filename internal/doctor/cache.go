package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/project"
)

const capabilityCacheSchema = 1

type capabilityKey struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Binary      string `json:"binary"`
	Size        int64  `json:"size"`
	ModTimeNano int64  `json:"mtime_unix_nano"`
	Version     string `json:"version"`
}

type capabilityDocument struct {
	Schema int           `json:"schema"`
	Key    capabilityKey `json:"key"`
	Checks []Check       `json:"checks"`
}

func capabilityKeyFor(binary, version string) (capabilityKey, error) {
	canonical, err := filepath.EvalSymlinks(binary)
	if err != nil || !filepath.IsAbs(canonical) {
		return capabilityKey{}, fmt.Errorf("resolve QEMU capability binary: %w", err)
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return capabilityKey{}, errors.New("QEMU capability binary is not a regular resolved file")
	}
	return capabilityKey{OS: runtime.GOOS, Arch: runtime.GOARCH, Binary: canonical, Size: info.Size(), ModTimeNano: info.ModTime().UnixNano(), Version: version}, nil
}

func (p Probe) capabilityDataRoot() (string, error) {
	workDir, err := p.workDir()
	if err != nil {
		return "", err
	}
	if projectValue, openErr := project.Open(workDir); openErr == nil {
		return projectValue.DataRoot, nil
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return "", openErr
	}
	return project.ResolveDataRoot(workDir, nil)
}

func (p Probe) capabilityCachePath() (string, error) {
	root, err := p.capabilityDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "cache", "capabilities", "qemu-"+runtime.GOOS+"-"+runtime.GOARCH+".json"), nil
}

func cacheOwner(info os.FileInfo) (int, bool) {
	value, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(value.Uid), true
}

func validateCacheChecks(checks []Check) error {
	if len(checks) != 5 {
		return errors.New("capability cache must contain five static checks")
	}
	wanted := []string{"accelerator", "machine", "cpu", "devices", "netdev"}
	for index, check := range checks {
		if check.Name != wanted[index] || (check.Status != OK && check.Status != Error) || check.Evidence == "" {
			return errors.New("capability cache contains an invalid check")
		}
	}
	return nil
}

func readCapabilityDocument(path string) (capabilityDocument, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return capabilityDocument{}, err
	}
	owner, ok := cacheOwner(info)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > 1<<20 || !ok || owner != os.Geteuid() {
		return capabilityDocument{}, errors.New("QEMU capability cache metadata is unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return capabilityDocument{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document capabilityDocument
	if err := decoder.Decode(&document); err != nil {
		return capabilityDocument{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return capabilityDocument{}, errors.New("QEMU capability cache has trailing JSON")
	}
	if document.Schema != capabilityCacheSchema {
		return capabilityDocument{}, errors.New("QEMU capability cache schema is unsupported")
	}
	if err := validateCacheChecks(document.Checks); err != nil {
		return capabilityDocument{}, err
	}
	return document, nil
}

func (p Probe) loadCapabilityCache(key capabilityKey) ([]Check, bool, error) {
	path, err := p.capabilityCachePath()
	if err != nil {
		return nil, false, err
	}
	if err := validateCapabilityDataRoot(filepath.Dir(filepath.Dir(filepath.Dir(path)))); err != nil {
		return nil, false, err
	}
	document, err := readCapabilityDocument(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if document.Key != key {
		return nil, false, nil
	}
	checks := append([]Check(nil), document.Checks...)
	for index := range checks {
		checks[index].Evidence = "cached for exact QEMU path/size/mtime/version: " + checks[index].Evidence
	}
	return checks, true, nil
}

func ensureCacheDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := cacheOwner(info)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || !ok || owner != os.Geteuid() {
		return fmt.Errorf("capability cache directory is unsafe: %s", path)
	}
	return os.Chmod(path, 0o700)
}

func validateCapabilityDataRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := cacheOwner(info)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || !ok || owner != os.Geteuid() || canonicalErr != nil || canonical != path {
		return errors.New("capability cache data root is unsafe")
	}
	return nil
}

func (p Probe) writeCapabilityCache(key capabilityKey, checks []Check) error {
	if err := validateCacheChecks(checks); err != nil {
		return err
	}
	path, err := p.capabilityCachePath()
	if err != nil {
		return err
	}
	dataRoot := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if err := validateCapabilityDataRoot(dataRoot); err != nil {
		return fmt.Errorf("capability cache data root is unavailable: %w", err)
	}
	for _, directory := range []string{filepath.Join(dataRoot, "cache"), filepath.Dir(path)} {
		if err := ensureCacheDirectory(directory); err != nil {
			return err
		}
	}
	document := capabilityDocument{Schema: capabilityCacheSchema, Key: key, Checks: checks}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, append(data, '\n'), 0o600)
}

package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNoConfig marks a project directory with no discoverable configuration.
var ErrNoConfig = errors.New("no configuration found; run `farrow setup` to create one, or pass -f")

// DiscoveryNames are the configuration filenames probed in the working
// directory, in order: an explicit farrow.yml wins over the Pigsty inventory
// it would otherwise share. The .yaml spellings are filename conveniences —
// the content format is the same inventory in every case.
var DiscoveryNames = []string{"farrow.yml", "farrow.yaml", "pigsty.yml", "pigsty.yaml"}

func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular non-symlink file: %s", path)
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, maxInventoryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInventoryBytes {
		return nil, fmt.Errorf("configuration exceeds the 4 MiB limit: %s", path)
	}
	return data, nil
}

// LoadPath parses one configuration file. The content decides the format: a
// Pigsty-compatible inventory (all:) parses and anything else is rejected.
func LoadPath(path string) (File, error) {
	if path == "" || !filepath.IsAbs(path) {
		return File{}, errors.New("configuration path must be absolute")
	}
	data, err := readBounded(path)
	if err != nil {
		return File{}, err
	}
	if _, err := DetectFormat(data); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	file, err := ParseInventory(data)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

// Discover resolves the project configuration: the explicit -f path when
// given, otherwise the first DiscoveryNames hit in the working directory.
// A missing configuration returns ErrNoConfig.
func Discover(cwd, explicit string) (File, string, error) {
	if explicit != "" {
		absolute, err := filepath.Abs(explicit)
		if err != nil {
			return File{}, "", err
		}
		file, err := LoadPath(absolute)
		return file, absolute, err
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return File{}, "", err
		}
	}
	for _, name := range DiscoveryNames {
		candidate := filepath.Join(cwd, name)
		if _, err := os.Lstat(candidate); err == nil {
			file, err := LoadPath(candidate)
			return file, candidate, err
		} else if !errors.Is(err, os.ErrNotExist) {
			return File{}, "", err
		}
	}
	return File{}, "", ErrNoConfig
}

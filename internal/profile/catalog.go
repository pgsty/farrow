// Package profile loads and applies policy to Piglet's embedded Pigsty
// profiles.
package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/pgsty/piglet/internal/config"
	profileassets "github.com/pgsty/piglet/profiles"
)

const (
	CatalogSchema = 3

	maxCatalogBytes = 64 << 10
)

var expectedNames = []string{
	"all", "citus", "deb", "deci", "dual", "full", "meta",
	"minio", "oss", "pro", "rpm", "simu", "trio",
}

// ErrNotFound identifies a profile name that is not present in the embedded
// catalog.
var ErrNotFound = errors.New("profile not found")

// ImagePolicy controls whether one image override may replace every node
// image without an explicit acknowledgement.
type ImagePolicy string

const (
	ImageHomogeneous ImagePolicy = "homogeneous"
	ImageMixed       ImagePolicy = "mixed"
)

// InventoryMode defines how a bound Pigsty inventory template relates to the
// Piglet VM topology.
type InventoryMode string

const (
	InventoryDirect      InventoryMode = "direct"
	InventoryBuildSubset InventoryMode = "build_subset"
)

// Descriptor records one Piglet-owned embedded profile and its override
// policy.
type Descriptor struct {
	Name                 string        `json:"name"`
	File                 string        `json:"file"`
	InventoryRef         string        `json:"inventory_ref"`
	InventoryMode        InventoryMode `json:"inventory_mode"`
	InventoryUnusedNodes []string      `json:"inventory_unused_nodes,omitempty"`
	Scalable             bool          `json:"scalable"`
	ImagePolicy          ImagePolicy   `json:"image_policy"`
}

// Catalog is the strict, versioned index of embedded profiles.
type Catalog struct {
	Schema   int          `json:"schema"`
	Profiles []Descriptor `json:"profiles"`
}

// LoadCatalog validates the catalog, the exact embedded YAML set, and every
// strict YAML configuration before returning any descriptors.
func LoadCatalog() (Catalog, error) {
	catalog, _, err := loadAll()
	return catalog, err
}

// List returns the catalog descriptors in deterministic name order.
func List() ([]Descriptor, error) {
	catalog, err := LoadCatalog()
	if err != nil {
		return nil, err
	}
	return append([]Descriptor(nil), catalog.Profiles...), nil
}

// Lookup returns one catalog descriptor.
func Lookup(name string) (Descriptor, error) {
	catalog, err := LoadCatalog()
	if err != nil {
		return Descriptor{}, err
	}
	for _, descriptor := range catalog.Profiles {
		if descriptor.Name == name {
			return cloneDescriptor(descriptor), nil
		}
	}
	return Descriptor{}, fmt.Errorf("%w: %q", ErrNotFound, name)
}

// Load returns one strictly decoded embedded configuration and its descriptor.
func Load(name string) (config.File, Descriptor, error) {
	_, loaded, err := loadAll()
	if err != nil {
		return config.File{}, Descriptor{}, err
	}
	entry, ok := loaded[name]
	if !ok {
		return config.File{}, Descriptor{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return cloneFile(entry.file), cloneDescriptor(entry.descriptor), nil
}

// YAML returns a copy of the original reviewable YAML bytes for init/export
// commands. The catalog and all configurations are validated first.
func YAML(name string) ([]byte, Descriptor, error) {
	_, loaded, err := loadAll()
	if err != nil {
		return nil, Descriptor{}, err
	}
	entry, ok := loaded[name]
	if !ok {
		return nil, Descriptor{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return append([]byte(nil), entry.yaml...), cloneDescriptor(entry.descriptor), nil
}

type loadedProfile struct {
	descriptor Descriptor
	file       config.File
	yaml       []byte
}

func loadAll() (Catalog, map[string]loadedProfile, error) {
	data, err := profileassets.FS.ReadFile("catalog.json")
	if err != nil {
		return Catalog{}, nil, fmt.Errorf("read embedded profile catalog: %w", err)
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, nil, errors.New("embedded profile catalog exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, nil, fmt.Errorf("decode strict profile catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Catalog{}, nil, errors.New("profile catalog must contain exactly one JSON value")
	}
	if err := validateCatalogMetadata(catalog); err != nil {
		return Catalog{}, nil, err
	}

	assetFiles, err := fs.Glob(profileassets.FS, "*.yaml")
	if err != nil {
		return Catalog{}, nil, fmt.Errorf("list embedded profile YAML: %w", err)
	}
	sort.Strings(assetFiles)
	wantedFiles := make([]string, 0, len(catalog.Profiles))
	loaded := make(map[string]loadedProfile, len(catalog.Profiles))
	for _, descriptor := range catalog.Profiles {
		wantedFiles = append(wantedFiles, descriptor.File)
		yamlData, err := profileassets.FS.ReadFile(descriptor.File)
		if err != nil {
			return Catalog{}, nil, fmt.Errorf("read embedded profile %s: %w", descriptor.Name, err)
		}
		file, err := config.Decode(bytes.NewReader(yamlData))
		if err != nil {
			return Catalog{}, nil, fmt.Errorf("decode embedded profile %s: %w", descriptor.Name, err)
		}
		if file.Name != descriptor.Name {
			return Catalog{}, nil, fmt.Errorf("embedded profile %s declares name %q", descriptor.File, file.Name)
		}
		loaded[descriptor.Name] = loadedProfile{
			descriptor: cloneDescriptor(descriptor),
			file:       file,
			yaml:       append([]byte(nil), yamlData...),
		}
	}
	sort.Strings(wantedFiles)
	if !equalStrings(assetFiles, wantedFiles) {
		return Catalog{}, nil, fmt.Errorf("embedded profile YAML set differs from catalog: embedded=%v catalog=%v", assetFiles, wantedFiles)
	}
	return catalog, loaded, nil
}

func validateCatalogMetadata(catalog Catalog) error {
	if catalog.Schema != CatalogSchema {
		return fmt.Errorf("profile catalog schema must be %d, got %d", CatalogSchema, catalog.Schema)
	}
	if len(catalog.Profiles) != len(expectedNames) {
		return fmt.Errorf("profile catalog requires exactly %d profiles, got %d", len(expectedNames), len(catalog.Profiles))
	}
	seen := make(map[string]struct{}, len(catalog.Profiles))
	for index, descriptor := range catalog.Profiles {
		if descriptor.Name != expectedNames[index] {
			return fmt.Errorf("profile catalog entry %d must be %q, got %q", index, expectedNames[index], descriptor.Name)
		}
		if _, exists := seen[descriptor.Name]; exists {
			return fmt.Errorf("duplicate profile %q", descriptor.Name)
		}
		seen[descriptor.Name] = struct{}{}
		if descriptor.File != descriptor.Name+".yaml" || path.Base(descriptor.File) != descriptor.File {
			return fmt.Errorf("profile %s has invalid embedded file %q", descriptor.Name, descriptor.File)
		}
		if !strings.HasPrefix(descriptor.InventoryRef, "conf/") || path.Clean(descriptor.InventoryRef) != descriptor.InventoryRef || strings.Contains(descriptor.InventoryRef, "..") {
			return fmt.Errorf("profile %s has invalid inventory reference %q", descriptor.Name, descriptor.InventoryRef)
		}
		expectedInventoryMode := InventoryDirect
		if descriptor.Name == "deb" || descriptor.Name == "rpm" {
			expectedInventoryMode = InventoryBuildSubset
		}
		if descriptor.InventoryMode != expectedInventoryMode {
			return fmt.Errorf("profile %s inventory mode must be %q", descriptor.Name, expectedInventoryMode)
		}
		expectedUnused := []string(nil)
		if descriptor.Name == "deci" {
			expectedUnused = []string{"node-8", "node-9"}
		}
		if !equalStrings(descriptor.InventoryUnusedNodes, expectedUnused) {
			return fmt.Errorf("profile %s unused inventory nodes must be %v", descriptor.Name, expectedUnused)
		}
		expectedScalable := descriptor.Name != "deci" && descriptor.Name != "simu"
		if descriptor.Scalable != expectedScalable {
			return fmt.Errorf("profile %s scalable policy does not match the migration contract", descriptor.Name)
		}
		expectedPolicy := ImageHomogeneous
		switch descriptor.Name {
		case "all", "deb", "oss", "pro", "rpm":
			expectedPolicy = ImageMixed
		}
		if descriptor.ImagePolicy != expectedPolicy {
			return fmt.Errorf("profile %s image policy must be %q", descriptor.Name, expectedPolicy)
		}
	}
	return nil
}

func cloneDescriptor(source Descriptor) Descriptor {
	result := source
	result.InventoryUnusedNodes = append([]string(nil), source.InventoryUnusedNodes...)
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

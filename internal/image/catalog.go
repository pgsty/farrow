package image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ManifestSchema          = 2
	EmbeddedManifestVersion = 2026082801
	MaxManifestSize         = 1 << 20
)

var embeddedManifestGeneratedAt = time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)

func reservedLocalNamespace(value string) bool {
	return value == "local" || strings.HasPrefix(value, localAliasPrefix)
}

type Artifact struct {
	File         string `json:"file"`
	Upstream     string `json:"upstream"`
	SHA256       string `json:"sha256"`
	Format       string `json:"format"`
	ArtifactSize int64  `json:"artifact_size"`
	VirtualSize  int64  `json:"virtual_size"`
	SourceUser   string `json:"source_user"`
	Boot         string `json:"boot"`
	Status       string `json:"status"`
	Provenance   string `json:"provenance"`
}

type CatalogImage struct {
	Aliases  []string                       `json:"aliases,omitempty"`
	Default  string                         `json:"default"`
	Releases map[string]map[string]Artifact `json:"releases"`
}

type Catalog struct {
	Schema      int                     `json:"schema"`
	Version     uint64                  `json:"version"`
	GeneratedAt time.Time               `json:"generated_at"`
	Images      map[string]CatalogImage `json:"images"`
}

var catalogName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

const localAliasPrefix = "local-"

func strictCatalog(data []byte) (Catalog, error) {
	if len(data) == 0 || len(data) > MaxManifestSize {
		return Catalog{}, fmt.Errorf("manifest size %d is outside 1..%d", len(data), MaxManifestSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode strict image manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("image manifest contains trailing JSON data")
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate() error {
	if c.Schema != ManifestSchema || c.Version == 0 || c.GeneratedAt.IsZero() || len(c.Images) == 0 {
		return errors.New("image manifest schema/version/time/images are invalid")
	}
	allNames := make(map[string]struct{})
	for name := range c.Images {
		if !catalogName.MatchString(name) {
			return fmt.Errorf("invalid image name %q", name)
		}
		if reservedLocalNamespace(name) {
			return fmt.Errorf("image name %q uses the reserved local image namespace", name)
		}
		allNames[name] = struct{}{}
	}
	for name, imageRecord := range c.Images {
		if len(imageRecord.Releases) == 0 || strings.TrimSpace(imageRecord.Default) == "" {
			return fmt.Errorf("image %s has no releases or default release", name)
		}
		if _, ok := imageRecord.Releases[imageRecord.Default]; !ok {
			return fmt.Errorf("image %s default release %q does not exist", name, imageRecord.Default)
		}
		for _, alias := range imageRecord.Aliases {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if !catalogName.MatchString(alias) {
				return fmt.Errorf("invalid image alias %q", alias)
			}
			if reservedLocalNamespace(alias) {
				return fmt.Errorf("image alias %q uses the reserved local image namespace", alias)
			}
			if _, exists := allNames[alias]; exists {
				return fmt.Errorf("duplicate image name/alias %q", alias)
			}
			allNames[alias] = struct{}{}
		}
		for release, architectures := range imageRecord.Releases {
			if strings.TrimSpace(release) == "" || len(architectures) == 0 {
				return fmt.Errorf("image %s has invalid release", name)
			}
			for arch, artifact := range architectures {
				if arch != "arm64" && arch != "amd64" {
					return fmt.Errorf("image %s release %s has unsupported arch %s", name, release, arch)
				}
				if !digestPattern.MatchString(artifact.SHA256) || artifact.Format != "qcow2" || artifact.ArtifactSize <= 0 || artifact.ArtifactSize > MaxArtifactSize || artifact.VirtualSize <= 0 || strings.TrimSpace(artifact.SourceUser) == "" || (artifact.Boot != "bios" && artifact.Boot != "uefi") || (artifact.Status != "supported" && artifact.Status != "testing" && artifact.Status != "deprecated") || strings.TrimSpace(artifact.Provenance) == "" {
					return fmt.Errorf("image %s release %s/%s has invalid artifact fields", name, release, arch)
				}
				if !validRepositoryFile(name, artifact.File) {
					return fmt.Errorf("image %s release %s/%s has unsafe repository file %q", name, release, arch, artifact.File)
				}
				parsed, err := url.Parse(artifact.Upstream)
				if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || hasMovingReleasePath(parsed.Path) {
					return fmt.Errorf("image %s release %s/%s upstream must be immutable absolute HTTPS", name, release, arch)
				}
			}
		}
	}
	return nil
}

var repositoryFilename = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,159}\.qcow2$`)

func validRepositoryFile(family, filename string) bool {
	if filename == "" || path.Clean(filename) != filename || strings.HasPrefix(filename, "/") {
		return false
	}
	parts := strings.Split(filename, "/")
	return len(parts) == 2 && parts[0] == family && repositoryFilename.MatchString(parts[1])
}

func hasMovingReleasePath(path string) bool {
	for _, segment := range strings.Split(strings.ToLower(path), "/") {
		if segment == "latest" || segment == "current" || segment == "release" || strings.Contains(segment, ".latest.") {
			return true
		}
	}
	return false
}

func (c Catalog) Entry(alias, arch string) (Entry, error) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	canonical := ""
	for name, record := range c.Images {
		if alias == name {
			canonical = name
			break
		}
		for _, candidate := range record.Aliases {
			if alias == candidate {
				canonical = name
				break
			}
		}
	}
	if canonical == "" {
		return Entry{}, fmt.Errorf("unknown image alias %q", alias)
	}
	record := c.Images[canonical]
	release := record.Default
	artifact, ok := record.Releases[release][arch]
	if ok {
		return Entry{Alias: canonical, Release: release, Arch: arch, File: artifact.File, Upstream: artifact.Upstream, SHA256: artifact.SHA256, Format: artifact.Format, ArtifactSize: artifact.ArtifactSize, VirtualSize: artifact.VirtualSize, SourceUser: artifact.SourceUser, Boot: artifact.Boot, Status: artifact.Status, Provenance: artifact.Provenance}, nil
	}
	return Entry{}, fmt.Errorf("image %s has no %s artifact", canonical, arch)
}

func (c Catalog) Entries() []Entry {
	result := make([]Entry, 0)
	imageNames := make([]string, 0, len(c.Images))
	for name := range c.Images {
		imageNames = append(imageNames, name)
	}
	sort.Strings(imageNames)
	for _, name := range imageNames {
		record := c.Images[name]
		releases := make([]string, 0, len(record.Releases))
		for release := range record.Releases {
			releases = append(releases, release)
		}
		sort.Strings(releases)
		for _, release := range releases {
			architectures := make([]string, 0, len(record.Releases[release]))
			for arch := range record.Releases[release] {
				architectures = append(architectures, arch)
			}
			sort.Strings(architectures)
			for _, arch := range architectures {
				artifact := record.Releases[release][arch]
				result = append(result, Entry{Alias: name, Release: release, Arch: arch, File: artifact.File, Upstream: artifact.Upstream, SHA256: artifact.SHA256, Format: artifact.Format, ArtifactSize: artifact.ArtifactSize, VirtualSize: artifact.VirtualSize, SourceUser: artifact.SourceUser, Boot: artifact.Boot, Status: artifact.Status, Provenance: artifact.Provenance})
			}
		}
	}
	return result
}

func EmbeddedCatalog() Catalog {
	catalog := Catalog{Schema: ManifestSchema, Version: EmbeddedManifestVersion, GeneratedAt: embeddedManifestGeneratedAt, Images: make(map[string]CatalogImage)}
	for _, entry := range EmbeddedEntries() {
		record := catalog.Images[entry.Alias]
		if record.Releases == nil {
			record.Releases = make(map[string]map[string]Artifact)
		}
		if record.Releases[entry.Release] == nil {
			record.Releases[entry.Release] = make(map[string]Artifact)
		}
		record.Releases[entry.Release][entry.Arch] = Artifact{File: entry.File, Upstream: entry.Upstream, SHA256: entry.SHA256, Format: entry.Format, ArtifactSize: entry.ArtifactSize, VirtualSize: entry.VirtualSize, SourceUser: entry.SourceUser, Boot: entry.Boot, Status: entry.Status, Provenance: entry.Provenance}
		record.Aliases = aliasesFor(entry.Alias)
		record.Default = entry.Release
		catalog.Images[entry.Alias] = record
	}
	return catalog
}

func aliasesFor(name string) []string {
	result := make([]string, 0)
	for alias, canonical := range aliases {
		if canonical == name {
			result = append(result, alias)
		}
	}
	sort.Strings(result)
	return result
}

func EmbeddedCatalogBytes() ([]byte, error) {
	data, err := json.MarshalIndent(EmbeddedCatalog(), "", "  ")
	return append(data, '\n'), err
}

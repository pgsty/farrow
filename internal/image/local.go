package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/lock"
)

const LocalAliasesSchema = 1

type LocalAlias struct {
	Name       string    `json:"name"`
	Digest     string    `json:"digest"`
	Arch       string    `json:"arch"`
	Boot       string    `json:"boot"`
	SourceUser string    `json:"source_user"`
	CreatedAt  time.Time `json:"created_at"`
}

type LocalAliases struct {
	Schema  int                   `json:"schema"`
	Aliases map[string]LocalAlias `json:"aliases"`
}

func (s Store) localAliasesPath() string {
	return filepath.Join(s.cacheDir(), "local-aliases.json")
}

func validateLocalAlias(value LocalAlias) error {
	if !catalogName.MatchString(value.Name) || !digestPattern.MatchString(value.Digest) || (value.Arch != "arm64" && value.Arch != "amd64") || (value.Boot != "bios" && value.Boot != "uefi") || strings.TrimSpace(value.SourceUser) == "" || value.CreatedAt.IsZero() {
		return errors.New("local image alias identity/digest/arch/boot/user/time is invalid")
	}
	return nil
}

func validateLocalAliases(value LocalAliases) error {
	if value.Schema != LocalAliasesSchema || value.Aliases == nil || len(value.Aliases) > 256 {
		return errors.New("local image alias registry schema or size is invalid")
	}
	for name, alias := range value.Aliases {
		if alias.Name != name {
			return fmt.Errorf("local image alias map key %q does not match entry %q", name, alias.Name)
		}
		if err := validateLocalAlias(alias); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) readLocalAliases() (LocalAliases, error) {
	path := s.localAliasesPath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return LocalAliases{Schema: LocalAliasesSchema, Aliases: make(map[string]LocalAlias)}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() > 1<<20 {
		return LocalAliases{}, errors.New("local image alias registry is unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalAliases{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value LocalAliases
	if err := decoder.Decode(&value); err != nil {
		return LocalAliases{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return LocalAliases{}, errors.New("local image alias registry has trailing JSON data")
	}
	if err := validateLocalAliases(value); err != nil {
		return LocalAliases{}, err
	}
	return value, nil
}

func localEntry(alias LocalAlias, metadata Metadata) Entry {
	return Entry{
		Alias: alias.Name, Release: "local-" + alias.Digest[:12], Arch: alias.Arch,
		SHA256: alias.Digest, Format: "qcow2", ArtifactSize: metadata.ArtifactSize, VirtualSize: metadata.VirtualSize,
		SourceUser: alias.SourceUser, Boot: alias.Boot, Status: "testing",
		Provenance: "explicit local import into the managed digest cache",
	}
}

func (s Store) RegisterLocalAlias(ctx context.Context, name, digest, arch, boot, sourceUser string) (Entry, string, Metadata, error) {
	if err := s.validate(); err != nil {
		return Entry{}, "", Metadata{}, err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	value := LocalAlias{Name: name, Digest: digest, Arch: arch, Boot: boot, SourceUser: strings.TrimSpace(sourceUser), CreatedAt: time.Now().UTC()}
	if err := validateLocalAlias(value); err != nil {
		return Entry{}, "", Metadata{}, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cacheLock, err := lock.Acquire(lockContext, s.lockPath(), false)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	defer cacheLock.Release()
	path, metadata, err := s.ValidateCached(ctx, digest)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	registry, err := s.readLocalAliases()
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	if existing, ok := registry.Aliases[name]; ok {
		if existing.Digest != digest || existing.Arch != arch || existing.Boot != boot || existing.SourceUser != value.SourceUser {
			return Entry{}, "", Metadata{}, fmt.Errorf("local image alias %q already names a different immutable image", name)
		}
		return localEntry(existing, metadata), path, metadata, nil
	}
	registry.Aliases[name] = value
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	if err := fsutil.AtomicWrite(s.localAliasesPath(), append(data, '\n'), 0o600); err != nil {
		return Entry{}, "", Metadata{}, err
	}
	return localEntry(value, metadata), path, metadata, nil
}

func (s Store) ResolveLocalAlias(ctx context.Context, name, arch string) (Entry, string, Metadata, error) {
	if err := s.validate(); err != nil {
		return Entry{}, "", Metadata{}, err
	}
	registry, err := s.readLocalAliases()
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	alias, ok := registry.Aliases[strings.ToLower(strings.TrimSpace(name))]
	if !ok || alias.Arch != arch {
		return Entry{}, "", Metadata{}, os.ErrNotExist
	}
	path, metadata, err := s.ValidateCached(ctx, alias.Digest)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	return localEntry(alias, metadata), path, metadata, nil
}

func (s Store) LocalEntries() ([]LocalAlias, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	registry, err := s.readLocalAliases()
	if err != nil {
		return nil, err
	}
	result := make([]LocalAlias, 0, len(registry.Aliases))
	for _, alias := range registry.Aliases {
		result = append(result, alias)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

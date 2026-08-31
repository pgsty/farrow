package image

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/activity"
	"github.com/pgsty/farrow/internal/execx"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/platform"
)

// Service is the one command-level façade over the catalog, the store, and
// the signed manifest chain. Callers resolve the data root; Service owns
// repository selection, catalog refresh, and store construction.
type Service struct {
	DataRoot   string
	Repository string
	QEMUImg    string
	Runner     execx.Runner
	Progress   activity.Reporter
}

// Info describes one catalog or local image with its cache state.
type Info struct {
	Entry    Entry         `json:"entry"`
	Manifest ManifestState `json:"manifest"`
	Cached   bool          `json:"cached"`
	Path     string        `json:"path,omitempty"`
	Metadata *Metadata     `json:"metadata,omitempty"`
}

// LookupArch resolves catalog metadata for an explicit architecture without
// refreshing the catalog or downloading an artifact. It is used by read-only
// lifecycle planning to validate boot firmware before reporting feasibility.
func (s Service) LookupArch(ctx context.Context, alias, arch string) (Entry, error) {
	if arch != "amd64" && arch != "arm64" {
		return Entry{}, fmt.Errorf("unsupported image architecture %q", arch)
	}
	_, explicit, err := s.configuredRepository()
	if err != nil {
		return Entry{}, err
	}
	catalog, _, repository, err := s.catalog(ctx, explicit)
	if err != nil {
		return Entry{}, err
	}
	entry, entryErr := catalog.Entry(alias, arch)
	if entryErr == nil {
		return entry, nil
	}
	store, err := s.store(repository)
	if err != nil {
		return Entry{}, err
	}
	localEntry, _, _, localErr := store.ResolveLocalAlias(ctx, alias, arch)
	if localErr != nil {
		return Entry{}, entryErr
	}
	return localEntry, nil
}

func (s Service) runner() execx.Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return execx.OSRunner{Timeout: 15 * time.Second, OutputLimit: 1 << 20}
}

func (s Service) report(phase, message string) {
	s.Progress.Report(activity.Event{Phase: phase, Message: message})
}

func (s Service) store(repository string) (Store, error) {
	qemuImg := s.QEMUImg
	if qemuImg == "" {
		found, err := exec.LookPath("qemu-img")
		if err != nil {
			return Store{}, err
		}
		qemuImg = found
	}
	return Store{DataRoot: s.DataRoot, Repository: repository, QEMUImg: qemuImg, Runner: s.runner(), Progress: s.Progress}, nil
}

func (s Service) configuredRepository() (string, bool, error) {
	repository := strings.TrimSpace(s.Repository)
	explicit := repository != ""
	if repository == "" {
		repository = strings.TrimSpace(os.Getenv("FARROW_REPO"))
		explicit = repository != ""
	}
	if repository == "" {
		repository = DefaultRepositoryURL
	}
	normalized, err := NormalizeRepository(repository)
	return normalized, explicit, err
}

// catalog refreshes the signed catalog from the configured repository when
// requested, falling back to the embedded catalog if the default repository is
// unreachable, and returns the active catalog plus the repository to pull from.
func (s Service) catalog(ctx context.Context, syncRepository bool) (Catalog, ManifestState, string, error) {
	repository, explicit, err := s.configuredRepository()
	if err != nil {
		return Catalog{}, ManifestState{}, "", err
	}
	manager := ManifestManager{DataRoot: s.DataRoot, Repository: repository, AllowUnsigned: explicit && RepositoryAllowsUnsigned(repository)}
	defaultSyncFailed := false
	if syncRepository && repository != "" {
		source, sourceErr := RepositoryCatalogSource(repository)
		if sourceErr != nil {
			return Catalog{}, ManifestState{}, "", sourceErr
		}
		s.Progress.Report(activity.Event{Phase: "image-catalog", Message: "Refreshing the image catalog", Source: source})
		if _, syncErr := manager.Sync(ctx, source, false); syncErr != nil {
			if explicit {
				return Catalog{}, ManifestState{}, "", fmt.Errorf("sync explicit image repository: %w", syncErr)
			}
			s.report("image-catalog", "Default image repository unavailable; using the embedded catalog")
			repository = ""
			defaultSyncFailed = true
		} else {
			s.Progress.Report(activity.Event{Phase: "image-catalog", Message: "Image catalog is current", Source: source, Done: true})
		}
	}
	catalog, state, err := manager.Current()
	if err != nil {
		return Catalog{}, ManifestState{}, "", err
	}
	if repository == "" && !defaultSyncFailed {
		repository = RepositoryFromCatalogSource(state.Source)
	}
	return catalog, state, repository, nil
}

// List returns every catalog entry plus registered local aliases.
func (s Service) List(ctx context.Context) ([]Entry, ManifestState, error) {
	catalog, manifestState, _, err := s.catalog(ctx, true)
	if err != nil {
		return nil, ManifestState{}, err
	}
	entries := catalog.Entries()
	// Listing is metadata-only. A stale or foreign-architecture local alias must
	// not make the entire catalog undiscoverable; info/pull perform byte checks.
	registry, err := (Store{DataRoot: s.DataRoot}).readLocalAliases()
	if err != nil {
		return nil, ManifestState{}, err
	}
	localNames := make([]string, 0, len(registry.Aliases))
	for name := range registry.Aliases {
		localNames = append(localNames, name)
	}
	sort.Strings(localNames)
	for _, name := range localNames {
		entries = append(entries, localEntry(registry.Aliases[name]))
	}
	return entries, manifestState, nil
}

// Info reports one alias without downloading anything.
func (s Service) Info(ctx context.Context, alias string) (Info, error) {
	profile, err := platform.Native()
	if err != nil {
		return Info{}, err
	}
	return s.InfoArch(ctx, alias, profile.Arch)
}

// InfoArch reports one alias for an explicit guest architecture without
// downloading it. Lifecycle warnings use this path so they describe the
// artifact that was actually selected rather than the host architecture.
func (s Service) InfoArch(ctx context.Context, alias, arch string) (Info, error) {
	if arch != "amd64" && arch != "arm64" {
		return Info{}, fmt.Errorf("unsupported image architecture %q", arch)
	}
	catalog, manifestState, repository, err := s.catalog(ctx, true)
	if err != nil {
		return Info{}, err
	}
	store, err := s.store(repository)
	if err != nil {
		return Info{}, err
	}
	entry, entryErr := catalog.Entry(alias, arch)
	if entryErr != nil {
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, arch)
		if localErr != nil {
			return Info{}, entryErr
		}
		return Info{Entry: localEntry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
	}
	path, metadata, cacheErr := store.ValidateCached(ctx, entry)
	if errors.Is(cacheErr, os.ErrNotExist) {
		return Info{Entry: entry, Manifest: manifestState}, nil
	}
	if cacheErr != nil {
		return Info{}, cacheErr
	}
	return Info{Entry: entry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
}

// PullAlias downloads and verifies one alias into the store.
func (s Service) PullAlias(ctx context.Context, alias string) (Info, error) {
	profile, err := platform.Native()
	if err != nil {
		return Info{}, err
	}
	return s.PullArch(ctx, alias, profile.Arch)
}

// PullArch downloads and verifies one reference for an explicit architecture.
func (s Service) PullArch(ctx context.Context, alias, arch string) (Info, error) {
	if arch != "amd64" && arch != "arm64" {
		return Info{}, fmt.Errorf("unsupported image architecture %q", arch)
	}
	catalog, manifestState, repository, err := s.catalog(ctx, true)
	if err != nil {
		return Info{}, err
	}
	store, err := s.store(repository)
	if err != nil {
		return Info{}, err
	}
	entry, entryErr := catalog.Entry(alias, arch)
	if entryErr != nil {
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, arch)
		if localErr != nil {
			return Info{}, entryErr
		}
		return Info{Entry: localEntry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
	}
	path, metadata, err := store.Pull(ctx, entry)
	if err != nil {
		return Info{}, err
	}
	return Info{Entry: entry, Manifest: manifestState, Cached: true, Path: path, Metadata: &metadata}, nil
}

// ResolveArch is the lifecycle seam for an explicit deployment guest
// architecture. It does not infer acceleration or fall back to another
// artifact architecture.
func (s Service) ResolveArch(ctx context.Context, alias, arch string) (Entry, string, Metadata, error) {
	if arch != "amd64" && arch != "arm64" {
		return Entry{}, "", Metadata{}, fmt.Errorf("unsupported image architecture %q", arch)
	}
	s.report("image-resolve", fmt.Sprintf("Resolving image %s for %s", alias, arch))
	catalog, _, repository, err := s.catalog(ctx, true)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	store, err := s.store(repository)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	entry, entryErr := catalog.Entry(alias, arch)
	if entryErr != nil {
		s.report("image-resolve", fmt.Sprintf("Looking for local image alias %s", alias))
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, arch)
		if localErr != nil {
			return Entry{}, "", Metadata{}, entryErr
		}
		s.Progress.Report(activity.Event{Phase: "image-ready", Message: fmt.Sprintf("Using local image %s (%s)", localEntry.Alias, arch), Done: true})
		return localEntry, path, metadata, nil
	}
	path, metadata, err := store.Pull(ctx, entry)
	return entry, path, metadata, err
}

// ImportFile verifies and installs a local qcow2 without registering an alias.
func (s Service) ImportFile(ctx context.Context, source, expectedDigest string) (string, Metadata, error) {
	store, err := s.store("")
	if err != nil {
		return "", Metadata{}, err
	}
	return store.Import(ctx, source, expectedDigest)
}

// ImportNamed verifies, installs, and registers a local alias.
func (s Service) ImportNamed(ctx context.Context, source, expectedDigest, name, boot, sourceUser string) (Entry, string, Metadata, error) {
	profile, err := platform.Native()
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	store, err := s.store("")
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	path, metadata, err := store.Import(ctx, source, expectedDigest)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	return store.RegisterLocalAlias(ctx, name, path, metadata, profile.Arch, boot, sourceUser)
}

func (s Service) manifestManager() (ManifestManager, error) {
	repository, explicit, err := s.configuredRepository()
	if err != nil {
		return ManifestManager{}, err
	}
	return ManifestManager{
		DataRoot:      s.DataRoot,
		Repository:    repository,
		AllowUnsigned: explicit && RepositoryAllowsUnsigned(repository),
	}, nil
}

// SyncManifest activates a signed catalog from a URL or local path.
func (s Service) SyncManifest(ctx context.Context, source string, allowDowngrade bool) (ManifestState, error) {
	manager, err := s.manifestManager()
	if err != nil {
		return ManifestState{}, err
	}
	return manager.Sync(ctx, source, allowDowngrade)
}

// ResetManifest restores the embedded bootstrap catalog.
func (s Service) ResetManifest(ctx context.Context) (ManifestState, error) {
	manager, err := s.manifestManager()
	if err != nil {
		return ManifestState{}, err
	}
	return manager.Reset(ctx)
}

// PruneAll removes unreferenced images and stale staging files. The caller
// supplies the node-referenced digests; the catalog and registered local
// aliases are always protected.
func (s Service) PruneAll(ctx context.Context, apply bool, nodeRefs func(context.Context) (map[string]struct{}, error)) (PruneReport, error) {
	store, err := s.store("")
	if err != nil {
		return PruneReport{}, err
	}
	resolve := func() (_ map[string]struct{}, returnErr error) {
		lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		allocator, err := lock.Acquire(lockContext, filepath.Join(s.DataRoot, "locks", "allocator.lock"), false)
		if err != nil {
			return nil, err
		}
		defer func() {
			returnErr = lock.JoinRelease(returnErr, allocator, "image reference allocator lock")
		}()
		references := make(map[string]struct{})
		catalog, _, _, err := s.catalog(ctx, false)
		if err != nil {
			return nil, err
		}
		for _, entry := range catalog.Entries() {
			references[entry.SHA256] = struct{}{}
		}
		if nodeRefs != nil {
			nodeReferences, refErr := nodeRefs(ctx)
			if refErr != nil {
				return nil, refErr
			}
			for digest := range nodeReferences {
				references[digest] = struct{}{}
			}
		}
		locals, localErr := store.LocalEntries()
		if localErr != nil {
			return nil, localErr
		}
		for _, local := range locals {
			references[local.Digest] = struct{}{}
		}
		return references, nil
	}
	return store.Prune(ctx, apply, resolve)
}

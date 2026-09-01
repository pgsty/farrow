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
	DataRoot       string
	Repository     string
	QEMUImg        string
	Runner         execx.Runner
	Progress       activity.Reporter
	Now            func() time.Time
	CatalogTTL     time.Duration
	RefreshBackoff time.Duration
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
	session, err := s.OpenCatalog(ctx, CatalogLocalOnly)
	if err != nil {
		return Entry{}, err
	}
	return session.LookupArch(ctx, alias, arch)
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

// List returns every catalog entry plus registered local aliases.
func (s Service) List(ctx context.Context) ([]Entry, ManifestState, error) {
	session, err := s.OpenCatalog(ctx, CatalogRefreshIfDue)
	if err != nil {
		return nil, ManifestState{}, err
	}
	entries := session.Entries()
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
	return entries, session.Manifest(), nil
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
	session, err := s.OpenCatalog(ctx, CatalogRefreshIfDue)
	if err != nil {
		return Info{}, err
	}
	return session.InfoArch(ctx, alias, arch)
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
	session, err := s.OpenCatalog(ctx, CatalogRefreshIfDue)
	if err != nil {
		return Info{}, err
	}
	return session.PullArch(ctx, alias, arch)
}

// ResolveArch is the lifecycle seam for an explicit deployment guest
// architecture. It does not infer acceleration or fall back to another
// artifact architecture.
func (s Service) ResolveArch(ctx context.Context, alias, arch string) (Entry, string, Metadata, error) {
	session, err := s.OpenCatalog(ctx, CatalogRefreshIfDue)
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	return session.ResolveArch(ctx, alias, arch)
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
	state, err := manager.Sync(ctx, source, allowDowngrade)
	if err != nil {
		return ManifestState{}, err
	}
	if err := manager.markCatalogChecked(ctx, s.clock()); err != nil {
		s.Progress.Report(activity.Event{
			Phase: "image-catalog", Message: fmt.Sprintf("Catalog revision %d was activated, but freshness state was not recorded: %v", state.ActiveVersion, err), Done: true, Warning: true,
		})
	}
	return state, nil
}

// UpdateCatalog bypasses the cache TTL and refreshes the configured repository
// exactly once. It is the service boundary used by the top-level update command.
func (s Service) UpdateCatalog(ctx context.Context) (CatalogRefreshResult, error) {
	session, err := s.OpenCatalog(ctx, CatalogRefreshNow)
	if err != nil {
		return CatalogRefreshResult{}, err
	}
	return session.Refresh(), nil
}

// ResetManifest restores the embedded bootstrap catalog.
func (s Service) ResetManifest(ctx context.Context) (ManifestState, error) {
	manager, err := s.manifestManager()
	if err != nil {
		return ManifestState{}, err
	}
	state, err := manager.Reset(ctx)
	if err != nil {
		return ManifestState{}, err
	}
	_, persistedErr := manager.readState()
	var freshnessErr error
	if errors.Is(persistedErr, os.ErrNotExist) {
		freshnessErr = manager.clearCatalogFreshness(ctx)
	} else if persistedErr != nil {
		freshnessErr = persistedErr
	} else {
		freshnessErr = manager.markCatalogChecked(ctx, s.clock())
	}
	if freshnessErr != nil {
		s.Progress.Report(activity.Event{
			Phase: "image-catalog", Message: "Catalog reset completed, but freshness state could not be updated: " + freshnessErr.Error(), Done: true, Warning: true,
		})
	}
	return state, nil
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
		session, err := s.OpenCatalog(ctx, CatalogLocalOnly)
		if err != nil {
			return nil, err
		}
		for _, entry := range session.Entries() {
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

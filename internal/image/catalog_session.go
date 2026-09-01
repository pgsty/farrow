package image

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pgsty/farrow/internal/activity"
)

type CatalogRefreshPolicy uint8

const (
	CatalogLocalOnly CatalogRefreshPolicy = iota
	CatalogRefreshIfDue
	CatalogRefreshNow
)

// CatalogSession pins one verified catalog revision and repository selection
// for a complete command. Every lookup and pull through the session therefore
// agrees on aliases, boot metadata, digests, and artifact locations.
type CatalogSession struct {
	service    Service
	catalog    Catalog
	manifest   ManifestState
	repository string
	refresh    CatalogRefreshResult
}

func (s Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Service) catalogTTL() time.Duration {
	if s.CatalogTTL != 0 {
		return s.CatalogTTL
	}
	return DefaultCatalogTTL
}

func (s Service) catalogRefreshBackoff() time.Duration {
	if s.RefreshBackoff != 0 {
		return s.RefreshBackoff
	}
	return CatalogRefreshBackoff
}

func (s Service) OpenCatalog(ctx context.Context, policy CatalogRefreshPolicy) (*CatalogSession, error) {
	manager, err := s.manifestManager()
	if err != nil {
		return nil, err
	}
	repository := manager.Repository
	refresh := CatalogRefreshResult{Repository: repository}
	if policy != CatalogLocalOnly {
		if repository == "" {
			if policy == CatalogRefreshNow {
				return nil, errors.New("image catalog update requires a configured repository")
			}
		} else {
			source, sourceErr := RepositoryCatalogSource(repository)
			if sourceErr != nil {
				return nil, sourceErr
			}
			refresh.Source = source
			if policy == CatalogRefreshNow {
				refresh, err = manager.refreshCatalogNow(ctx, source, s.clock())
				if err != nil {
					return nil, err
				}
			} else {
				refresh, err = manager.refreshCatalogIfDue(ctx, source, s.clock(), s.catalogTTL(), s.catalogRefreshBackoff())
				if err != nil {
					refresh.Warning = err.Error()
				}
			}
		}
	}
	catalog, manifest, err := manager.Current()
	if err != nil {
		return nil, err
	}
	if refresh.PreviousVersion == 0 && !refresh.Attempted {
		refresh.PreviousVersion = catalog.Version
	}
	refresh.ActiveVersion = catalog.Version
	if refresh.Warning != "" {
		s.Progress.Report(activity.Event{
			Phase: "image-catalog", Message: "Image catalog refresh warning; using the trusted local catalog: " + refresh.Warning, Done: true, Warning: true,
		})
	}
	if refresh.Attempted && refresh.Warning == "" {
		message := fmt.Sprintf("Image catalog is current at revision %d", catalog.Version)
		if refresh.Updated {
			message = fmt.Sprintf("Updated image catalog to revision %d", catalog.Version)
		}
		s.Progress.Report(activity.Event{Phase: "image-catalog", Message: message, Source: refresh.Source, Done: true})
	}
	return &CatalogSession{service: s, catalog: catalog, manifest: manifest, repository: repository, refresh: refresh}, nil
}

func (session *CatalogSession) Manifest() ManifestState { return session.manifest }

func (session *CatalogSession) Refresh() CatalogRefreshResult { return session.refresh }

func (session *CatalogSession) Entries() []Entry { return session.catalog.Entries() }

func validateCatalogArch(arch string) error {
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported image architecture %q", arch)
	}
	return nil
}

func (session *CatalogSession) store() (Store, error) {
	return session.service.store(session.repository)
}

func (session *CatalogSession) LookupArch(ctx context.Context, alias, arch string) (Entry, error) {
	if err := validateCatalogArch(arch); err != nil {
		return Entry{}, err
	}
	entry, entryErr := session.catalog.Entry(alias, arch)
	if entryErr == nil {
		return entry, nil
	}
	store, err := session.store()
	if err != nil {
		return Entry{}, err
	}
	localEntry, _, _, localErr := store.ResolveLocalAlias(ctx, alias, arch)
	if localErr != nil {
		return Entry{}, entryErr
	}
	return localEntry, nil
}

func (session *CatalogSession) InfoArch(ctx context.Context, alias, arch string) (Info, error) {
	if err := validateCatalogArch(arch); err != nil {
		return Info{}, err
	}
	store, err := session.store()
	if err != nil {
		return Info{}, err
	}
	entry, entryErr := session.catalog.Entry(alias, arch)
	if entryErr != nil {
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, arch)
		if localErr != nil {
			return Info{}, entryErr
		}
		return Info{Entry: localEntry, Manifest: session.manifest, Cached: true, Path: path, Metadata: &metadata}, nil
	}
	path, metadata, cacheErr := store.ValidateCached(ctx, entry)
	if errors.Is(cacheErr, os.ErrNotExist) {
		return Info{Entry: entry, Manifest: session.manifest}, nil
	}
	if cacheErr != nil {
		return Info{}, cacheErr
	}
	return Info{Entry: entry, Manifest: session.manifest, Cached: true, Path: path, Metadata: &metadata}, nil
}

func (session *CatalogSession) PullArch(ctx context.Context, alias, arch string) (Info, error) {
	if err := validateCatalogArch(arch); err != nil {
		return Info{}, err
	}
	store, err := session.store()
	if err != nil {
		return Info{}, err
	}
	entry, entryErr := session.catalog.Entry(alias, arch)
	if entryErr != nil {
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, arch)
		if localErr != nil {
			return Info{}, entryErr
		}
		return Info{Entry: localEntry, Manifest: session.manifest, Cached: true, Path: path, Metadata: &metadata}, nil
	}
	path, metadata, err := store.Pull(ctx, entry)
	if err != nil {
		return Info{}, err
	}
	return Info{Entry: entry, Manifest: session.manifest, Cached: true, Path: path, Metadata: &metadata}, nil
}

func (session *CatalogSession) ResolveArch(ctx context.Context, alias, arch string) (Entry, string, Metadata, error) {
	if err := validateCatalogArch(arch); err != nil {
		return Entry{}, "", Metadata{}, err
	}
	session.service.report("image-resolve", fmt.Sprintf("Resolving image %s for %s", alias, arch))
	store, err := session.store()
	if err != nil {
		return Entry{}, "", Metadata{}, err
	}
	entry, entryErr := session.catalog.Entry(alias, arch)
	if entryErr != nil {
		session.service.report("image-resolve", fmt.Sprintf("Looking for local image alias %s", alias))
		localEntry, path, metadata, localErr := store.ResolveLocalAlias(ctx, alias, arch)
		if localErr != nil {
			return Entry{}, "", Metadata{}, entryErr
		}
		session.service.Progress.Report(activity.Event{Phase: "image-ready", Message: fmt.Sprintf("Using local image %s (%s)", localEntry.Alias, arch), Done: true})
		return localEntry, path, metadata, nil
	}
	path, metadata, err := store.Pull(ctx, entry)
	return entry, path, metadata, err
}

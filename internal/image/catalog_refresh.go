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
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/lock"
)

const (
	DefaultCatalogTTL         = 7 * 24 * time.Hour
	CatalogRefreshBackoff     = time.Hour
	catalogFreshnessSchema    = 1
	maxCatalogFreshnessSize   = 1 << 20
	catalogRefreshLockTimeout = 30 * time.Second
)

// CatalogRefreshResult describes one repository freshness decision. Automatic
// callers may carry Warning while continuing from a trusted local snapshot;
// an explicit update returns the same underlying failure instead.
type CatalogRefreshResult struct {
	Repository      string    `json:"repository"`
	Source          string    `json:"source"`
	Attempted       bool      `json:"attempted"`
	Updated         bool      `json:"updated"`
	PreviousVersion uint64    `json:"previous_version"`
	ActiveVersion   uint64    `json:"active_version"`
	CheckedAt       time.Time `json:"checked_at,omitempty"`
	Warning         string    `json:"warning,omitempty"`
}

type catalogFreshness struct {
	CheckedAt     time.Time `json:"checked_at,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
}

type catalogFreshnessRegistry struct {
	Schema       int                         `json:"schema"`
	Repositories map[string]catalogFreshness `json:"repositories"`
}

func (m ManifestManager) freshnessPath() string {
	return filepath.Join(m.root(), "freshness.json")
}

func (m ManifestManager) refreshLockPath() string {
	return filepath.Join(m.DataRoot, "locks", "catalog-refresh.lock")
}

func emptyCatalogFreshnessRegistry() catalogFreshnessRegistry {
	return catalogFreshnessRegistry{Schema: catalogFreshnessSchema, Repositories: make(map[string]catalogFreshness)}
}

func (m ManifestManager) readCatalogFreshness() (catalogFreshnessRegistry, error) {
	data, err := readLimitedFile(m.freshnessPath(), maxCatalogFreshnessSize)
	if errors.Is(err, os.ErrNotExist) {
		return emptyCatalogFreshnessRegistry(), nil
	}
	if err != nil {
		return catalogFreshnessRegistry{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var registry catalogFreshnessRegistry
	if err := decoder.Decode(&registry); err != nil {
		return catalogFreshnessRegistry{}, fmt.Errorf("decode catalog freshness state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return catalogFreshnessRegistry{}, errors.New("catalog freshness state has trailing JSON")
	}
	if registry.Schema != catalogFreshnessSchema || registry.Repositories == nil {
		return catalogFreshnessRegistry{}, errors.New("catalog freshness state schema or repositories are invalid")
	}
	for key, freshness := range registry.Repositories {
		if !manifestRepositoryKey.MatchString(key) {
			return catalogFreshnessRegistry{}, fmt.Errorf("catalog freshness repository key %q is invalid", key)
		}
		if freshness.CheckedAt.IsZero() && freshness.LastAttemptAt.IsZero() {
			return catalogFreshnessRegistry{}, fmt.Errorf("catalog freshness repository %s has no timestamps", key)
		}
	}
	return registry, nil
}

func (m ManifestManager) writeCatalogFreshness(held *lock.File, freshness catalogFreshness) error {
	if err := held.ValidateExclusive(m.refreshLockPath()); err != nil {
		return err
	}
	registry, err := m.readCatalogFreshness()
	if err != nil {
		// Freshness controls network scheduling, not catalog trust. A regular but
		// malformed or newer-version file must not permanently disable update;
		// AtomicWrite below still refuses unsafe symlink and directory targets.
		registry = emptyCatalogFreshnessRegistry()
	}
	key, err := m.stateKey()
	if err != nil {
		return err
	}
	registry.Repositories[key] = freshness
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(m.freshnessPath(), append(data, '\n'), 0o600)
}

func freshnessRecent(now, timestamp time.Time, duration time.Duration) bool {
	if timestamp.IsZero() || duration <= 0 {
		return false
	}
	age := now.Sub(timestamp)
	return age >= 0 && age < duration
}

func (m ManifestManager) catalogRefresh(
	ctx context.Context,
	source string,
	force bool,
	now time.Time,
	ttl time.Duration,
	backoff time.Duration,
) (_ CatalogRefreshResult, returnErr error) {
	if now.IsZero() {
		return CatalogRefreshResult{}, errors.New("catalog refresh clock is required")
	}
	if !force && (ttl <= 0 || backoff <= 0) {
		return CatalogRefreshResult{}, errors.New("catalog refresh TTL and failure backoff must be positive")
	}
	if err := m.validate(); err != nil {
		return CatalogRefreshResult{}, err
	}
	repository, err := NormalizeRepository(m.Repository)
	if err != nil {
		return CatalogRefreshResult{}, err
	}
	result := CatalogRefreshResult{Repository: repository, Source: source}
	lockContext, cancel := context.WithTimeout(ctx, catalogRefreshLockTimeout)
	defer cancel()
	refreshLock, err := lock.Acquire(lockContext, m.refreshLockPath(), false)
	if err != nil {
		return result, err
	}
	defer func() {
		returnErr = lock.JoinRelease(returnErr, refreshLock, "catalog refresh lock")
	}()
	registry, err := m.readCatalogFreshness()
	if err != nil {
		registry = emptyCatalogFreshnessRegistry()
	}
	key, err := m.stateKey()
	if err != nil {
		return result, err
	}
	freshness := registry.Repositories[key]
	result.CheckedAt = freshness.CheckedAt
	if !force {
		if freshnessRecent(now, freshness.CheckedAt, ttl) || freshnessRecent(now, freshness.LastAttemptAt, backoff) {
			return result, nil
		}
	}
	var current ManifestState
	if catalog, currentState, currentErr := m.Current(); currentErr == nil {
		result.PreviousVersion = catalog.Version
		result.ActiveVersion = catalog.Version
		current = currentState
	}
	result.Attempted = true
	freshness.LastAttemptAt = now.UTC()
	if err := m.writeCatalogFreshness(refreshLock, freshness); err != nil {
		result.Warning = "catalog refresh attempt could not be recorded: " + err.Error()
	}
	accepted, err := m.Sync(ctx, source, false)
	if err != nil {
		return result, err
	}
	freshness.CheckedAt = now.UTC()
	if err := m.writeCatalogFreshness(refreshLock, freshness); err != nil {
		message := fmt.Sprintf("catalog revision %d activated but freshness state was not recorded: %v", accepted.ActiveVersion, err)
		if result.Warning != "" {
			message = result.Warning + "; " + message
		}
		result.Warning = message
	}
	result.ActiveVersion = accepted.ActiveVersion
	result.Updated = accepted.ActiveVersion != current.ActiveVersion || accepted.ActiveDigest != current.ActiveDigest
	result.CheckedAt = freshness.CheckedAt
	return result, nil
}

func (m ManifestManager) refreshCatalogIfDue(ctx context.Context, source string, now time.Time, ttl, backoff time.Duration) (CatalogRefreshResult, error) {
	return m.catalogRefresh(ctx, source, false, now, ttl, backoff)
}

func (m ManifestManager) refreshCatalogNow(ctx context.Context, source string, now time.Time) (CatalogRefreshResult, error) {
	return m.catalogRefresh(ctx, source, true, now, DefaultCatalogTTL, CatalogRefreshBackoff)
}

func (m ManifestManager) markCatalogChecked(ctx context.Context, now time.Time) (returnErr error) {
	if now.IsZero() {
		return errors.New("catalog freshness clock is required")
	}
	if err := m.validate(); err != nil {
		return err
	}
	lockContext, cancel := context.WithTimeout(ctx, catalogRefreshLockTimeout)
	defer cancel()
	refreshLock, err := lock.Acquire(lockContext, m.refreshLockPath(), false)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = lock.JoinRelease(returnErr, refreshLock, "catalog refresh lock")
	}()
	registry, err := m.readCatalogFreshness()
	if err != nil {
		registry = emptyCatalogFreshnessRegistry()
	}
	key, err := m.stateKey()
	if err != nil {
		return err
	}
	freshness := registry.Repositories[key]
	freshness.CheckedAt = now.UTC()
	freshness.LastAttemptAt = now.UTC()
	return m.writeCatalogFreshness(refreshLock, freshness)
}

func (m ManifestManager) clearCatalogFreshness(ctx context.Context) (returnErr error) {
	if err := m.validate(); err != nil {
		return err
	}
	lockContext, cancel := context.WithTimeout(ctx, catalogRefreshLockTimeout)
	defer cancel()
	refreshLock, err := lock.Acquire(lockContext, m.refreshLockPath(), false)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = lock.JoinRelease(returnErr, refreshLock, "catalog refresh lock")
	}()
	registry, readErr := m.readCatalogFreshness()
	if readErr != nil {
		registry = emptyCatalogFreshnessRegistry()
	}
	key, err := m.stateKey()
	if err != nil {
		return err
	}
	if _, exists := registry.Repositories[key]; !exists && readErr == nil {
		return nil
	}
	delete(registry.Repositories, key)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	if err := refreshLock.ValidateExclusive(m.refreshLockPath()); err != nil {
		return err
	}
	return fsutil.AtomicWrite(m.freshnessPath(), append(data, '\n'), 0o600)
}

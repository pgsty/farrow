package image

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/lock"
)

type PruneItem struct {
	Digest       string `json:"digest"`
	ImagePath    string `json:"image_path"`
	MetadataPath string `json:"metadata_path"`
	Bytes        int64  `json:"bytes"`
	Applied      bool   `json:"applied"`
}

type PruneReport struct {
	Apply      bool        `json:"apply"`
	Referenced []string    `json:"referenced"`
	Items      []PruneItem `json:"items"`
}

func (s Store) scanPrune(ctx context.Context, referenced map[string]struct{}, apply bool) (PruneReport, error) {
	report := PruneReport{Apply: apply, Referenced: make([]string, 0, len(referenced)), Items: []PruneItem{}}
	for digest := range referenced {
		report.Referenced = append(report.Referenced, digest)
	}
	sort.Strings(report.Referenced)
	entries, err := os.ReadDir(s.cacheDir())
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	pairs := make(map[string]map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		extension := filepath.Ext(name)
		if extension != ".qcow2" && extension != ".json" {
			continue
		}
		digest := strings.TrimSuffix(name, extension)
		if !digestPattern.MatchString(digest) {
			return report, fmt.Errorf("cache contains malformed managed entry %q", name)
		}
		if pairs[digest] == nil {
			pairs[digest] = make(map[string]string)
		}
		pairs[digest][extension] = filepath.Join(s.cacheDir(), name)
	}
	digests := make([]string, 0, len(pairs))
	for digest := range pairs {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	for _, digest := range digests {
		pair := pairs[digest]
		if pair[".qcow2"] == "" || pair[".json"] == "" {
			return report, fmt.Errorf("cache digest %s does not have an exact image/metadata pair", digest)
		}
		_, metadata, err := s.ValidateCached(ctx, digest)
		if err != nil {
			return report, err
		}
		if _, keep := referenced[digest]; keep {
			continue
		}
		item := PruneItem{Digest: digest, ImagePath: pair[".qcow2"], MetadataPath: pair[".json"], Bytes: metadata.ArtifactSize}
		if apply {
			if err := os.Remove(item.ImagePath); err != nil {
				return report, err
			}
			if err := os.Remove(item.MetadataPath); err != nil {
				return report, err
			}
			if err := fsutil.SyncDir(s.cacheDir()); err != nil {
				return report, err
			}
			item.Applied = true
		}
		report.Items = append(report.Items, item)
	}
	return report, nil
}

func (s Store) Prune(ctx context.Context, apply bool, resolveReferences func() (map[string]struct{}, error)) (PruneReport, error) {
	if s.DataRoot == "" || !filepath.IsAbs(s.DataRoot) || s.QEMUImg == "" || s.Runner == nil {
		return PruneReport{}, errors.New("image prune store is incomplete")
	}
	if resolveReferences == nil {
		return PruneReport{}, errors.New("image prune reference resolver is required")
	}
	cacheInfo, err := os.Lstat(s.cacheDir())
	if errors.Is(err, os.ErrNotExist) {
		return PruneReport{Apply: apply, Items: []PruneItem{}}, nil
	}
	if err != nil || !cacheInfo.IsDir() || cacheInfo.Mode()&os.ModeSymlink != 0 || cacheInfo.Mode().Perm() != 0o700 {
		return PruneReport{}, errors.New("image cache directory is unsafe")
	}
	if !apply {
		referenced, err := resolveReferences()
		if err != nil {
			return PruneReport{}, err
		}
		return s.scanPrune(ctx, referenced, false)
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cacheLock, err := lock.Acquire(lockContext, s.lockPath(), false)
	if err != nil {
		return PruneReport{}, err
	}
	defer cacheLock.Release()
	referenced, err := resolveReferences()
	if err != nil {
		return PruneReport{}, err
	}
	return s.scanPrune(ctx, referenced, true)
}

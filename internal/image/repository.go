package image

import (
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

const CatalogFilename = "catalog.json"

// DefaultRepositoryURL is the public signed-catalog and artifact mirror used
// when neither --repo nor FARROW_REPO selects another repository. If this
// repository is unavailable, Service falls back to the signed embedded catalog
// and its immutable HTTPS upstream artifacts.
var DefaultRepositoryURL = "https://repo.pigsty.cc/farrow"

// RepositoryAllowsUnsigned reports whether an explicitly selected repository
// may rely on local ownership or HTTPS rather than a detached trusted
// signature. The compiled default remains in the signed embedded trust domain
// even when a user spells the same URL explicitly.
func RepositoryAllowsUnsigned(value string) bool {
	normalized, err := NormalizeRepository(value)
	if err != nil || normalized == "" {
		return false
	}
	if DefaultRepositoryURL != "" {
		defaultRepository, defaultErr := NormalizeRepository(DefaultRepositoryURL)
		if defaultErr == nil && normalized == defaultRepository {
			return false
		}
	}
	parsed, _ := url.Parse(normalized)
	return parsed.Scheme == "" || parsed.Scheme == "https"
}

func NormalizeRepository(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" {
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("image repository must be an absolute HTTP(S) URL without credentials, query, or fragment")
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		return parsed.String(), nil
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("local image repository must be an absolute directory")
	}
	return filepath.Clean(value), nil
}

func RepositoryCatalogSource(repository string) (string, error) {
	repository, err := NormalizeRepository(repository)
	if err != nil || repository == "" {
		return "", err
	}
	parsed, _ := url.Parse(repository)
	if parsed.Scheme != "" {
		parsed.Path = path.Join(parsed.Path, CatalogFilename)
		return parsed.String(), nil
	}
	return filepath.Join(repository, CatalogFilename), nil
}

func RepositoryArtifactSource(repository, filename string) (string, error) {
	repository, err := NormalizeRepository(repository)
	if err != nil || repository == "" {
		return "", err
	}
	if !safeRepositoryFile(filename) {
		return "", errors.New("image repository artifact path is unsafe")
	}
	parsed, _ := url.Parse(repository)
	if parsed.Scheme != "" {
		parsed.Path = path.Join(parsed.Path, filename)
		return parsed.String(), nil
	}
	return filepath.Join(repository, filepath.FromSlash(filename)), nil
}

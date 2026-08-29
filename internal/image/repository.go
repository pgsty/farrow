package image

import (
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

const CatalogFilename = "catalog.json"

// DefaultRepositoryURL is intentionally empty in ordinary public builds. They
// start from the signed embedded catalog and fetch its immutable HTTPS upstream
// artifacts without first probing a private development host. A release may
// override this with -ldflags -X only after a public mirror is live; FARROW_REPO
// and --repo remain explicit runtime overrides.
var DefaultRepositoryURL string

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

func RepositoryFromCatalogSource(source string) string {
	if strings.HasPrefix(source, "local:") {
		local := strings.TrimPrefix(source, "local:")
		if filepath.Base(local) == CatalogFilename {
			return filepath.Dir(local)
		}
		return ""
	}
	parsed, err := url.Parse(source)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || path.Base(parsed.Path) != CatalogFilename {
		return ""
	}
	parsed.Path = path.Dir(parsed.Path)
	if parsed.Path == "." || parsed.Path == "/" {
		parsed.Path = ""
	}
	return strings.TrimSuffix(parsed.String(), "/")
}

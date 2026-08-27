package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	darwinnet "github.com/pgsty/farrow/internal/network/darwin"
)

const maxSocketVMNetArchive = 4 << 20

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type DownloadResult struct {
	Path       string `json:"path"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	Downloaded bool   `json:"downloaded"`
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many socket_vmnet redirects")
			}
			if request.URL.Scheme != "https" {
				return errors.New("socket_vmnet redirect is not HTTPS")
			}
			return nil
		},
	}
}

func ensureCacheDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("setup cache directory must be a clean absolute path")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("setup cache is not a real directory: %s", path)
	}
	return os.Chmod(path, 0o700)
}

func DefaultCacheDirectory() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "farrow", "downloads"), nil
}

// DownloadPinnedSocketVMNet fetches only the embedded release URL, bounds the
// response, verifies the embedded digest and archive structure, then atomically
// publishes a user-owned cache entry. An already verified entry is reused.
func DownloadPinnedSocketVMNet(ctx context.Context, arch, cacheDirectory string, client HTTPDoer) (DownloadResult, error) {
	release, err := darwinnet.PinnedRelease(arch)
	if err != nil {
		return DownloadResult{}, err
	}
	return downloadSocketVMNetRelease(ctx, release, arch, cacheDirectory, client, func(path, arch string) error {
		_, err := darwinnet.VerifyArchive(path, arch)
		return err
	})
}

func downloadSocketVMNetRelease(ctx context.Context, release darwinnet.Release, arch, cacheDirectory string, client HTTPDoer, verify func(string, string) error) (DownloadResult, error) {
	parsed, err := url.Parse(release.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return DownloadResult{}, errors.New("embedded socket_vmnet URL is invalid")
	}
	if verify == nil {
		return DownloadResult{}, errors.New("socket_vmnet verifier is missing")
	}
	if release.ArchiveName == "" || filepath.Base(release.ArchiveName) != release.ArchiveName || len(release.SHA256) != 64 {
		return DownloadResult{}, errors.New("embedded socket_vmnet identity is invalid")
	}
	if cacheDirectory == "" {
		cacheDirectory, err = DefaultCacheDirectory()
		if err != nil {
			return DownloadResult{}, err
		}
	}
	if err := ensureCacheDirectory(cacheDirectory); err != nil {
		return DownloadResult{}, err
	}
	target := filepath.Join(cacheDirectory, release.SHA256+"-"+release.ArchiveName)
	result := DownloadResult{Path: target, URL: release.URL, SHA256: release.SHA256}
	if verifyErr := verify(target, arch); verifyErr == nil {
		return result, nil
	} else if info, statErr := os.Lstat(target); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("cached socket_vmnet archive is unsafe: %w", verifyErr)
		}
		if removeErr := os.Remove(target); removeErr != nil {
			return result, fmt.Errorf("remove invalid owned socket_vmnet cache entry: %w", removeErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	}
	if client == nil {
		client = defaultHTTPClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("User-Agent", "farrow-setup/"+release.Version)
	response, err := client.Do(request)
	if err != nil {
		return result, fmt.Errorf("download socket_vmnet: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && response.Request.URL != nil && response.Request.URL.Scheme != "https" {
		return result, errors.New("socket_vmnet response URL is not HTTPS")
	}
	if response.StatusCode != http.StatusOK || response.ContentLength > maxSocketVMNetArchive {
		return result, fmt.Errorf("socket_vmnet download failed status/size policy: %s", response.Status)
	}
	temporary, err := os.CreateTemp(cacheDirectory, ".socket-vmnet-*.partial")
	if err != nil {
		return result, err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return result, err
	}
	written, err := io.Copy(temporary, io.LimitReader(response.Body, maxSocketVMNetArchive+1))
	if err != nil {
		return result, fmt.Errorf("download socket_vmnet: %w", err)
	}
	if written > maxSocketVMNetArchive || (response.ContentLength >= 0 && written != response.ContentLength) {
		return result, errors.New("socket_vmnet download size differs from the bounded response")
	}
	if err := temporary.Sync(); err != nil {
		return result, err
	}
	if err := temporary.Close(); err != nil {
		return result, err
	}
	if err := verify(temporaryPath, arch); err != nil {
		return result, fmt.Errorf("verify downloaded socket_vmnet: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return result, err
	}
	published = true
	result.Downloaded = true
	return result, nil
}

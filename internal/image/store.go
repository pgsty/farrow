package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pgsty/piglet/internal/disk"
	"github.com/pgsty/piglet/internal/execx"
	"github.com/pgsty/piglet/internal/fsutil"
	"github.com/pgsty/piglet/internal/lock"
)

const (
	MetadataSchema        = 1
	MaxArtifactSize int64 = 8 << 30
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Metadata struct {
	Schema       int       `json:"schema"`
	Digest       string    `json:"digest"`
	Format       string    `json:"format"`
	VirtualSize  int64     `json:"virtual_size"`
	ArtifactSize int64     `json:"artifact_size"`
	Source       string    `json:"source"`
	ImportedAt   time.Time `json:"imported_at"`
}

type Store struct {
	DataRoot   string
	QEMUImg    string
	Runner     execx.Runner
	HTTPClient *http.Client
}

func (s Store) cacheDir() string { return filepath.Join(s.DataRoot, "cache", "images", "sha256") }
func (s Store) lockPath() string { return filepath.Join(s.DataRoot, "locks", "cache.lock") }

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("cache path is unsafe: %s", path)
	}
	return os.Chmod(path, 0o700)
}

func (s Store) validate() error {
	if s.DataRoot == "" || !filepath.IsAbs(s.DataRoot) || s.QEMUImg == "" || s.Runner == nil {
		return errors.New("image store data root, qemu-img, and runner are required")
	}
	for _, directory := range []string{s.DataRoot, s.cacheDir(), filepath.Dir(s.lockPath())} {
		if err := ensurePrivateDir(directory); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) Path(digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("invalid image digest %q", digest)
	}
	return filepath.Join(s.cacheDir(), digest+".qcow2"), nil
}

func (s Store) metadataPath(digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("invalid image digest %q", digest)
	}
	return filepath.Join(s.cacheDir(), digest+".json"), nil
}

func digestFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("image must be a regular non-symlink file: %s", path)
	}
	if info.Size() > MaxArtifactSize {
		return "", 0, fmt.Errorf("image size %d exceeds limit %d", info.Size(), MaxArtifactSize)
	}
	handle, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer handle.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(handle, MaxArtifactSize+1))
	if err != nil {
		return "", written, err
	}
	if written > MaxArtifactSize {
		return "", written, errors.New("image exceeded size limit while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func (s Store) manager() disk.Manager { return disk.Manager{QEMUImg: s.QEMUImg, Runner: s.Runner} }

func (s Store) ValidateCached(ctx context.Context, digest string) (string, Metadata, error) {
	path, err := s.Path(digest)
	if err != nil {
		return "", Metadata{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", Metadata{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 {
		return "", Metadata{}, errors.New("cached image is not an immutable regular file")
	}
	actual, size, err := digestFile(path)
	if err != nil || actual != digest {
		return "", Metadata{}, fmt.Errorf("cached image digest mismatch: got %s want %s: %w", actual, digest, err)
	}
	imageInfo, err := s.manager().Inspect(ctx, path)
	if err != nil {
		return "", Metadata{}, err
	}
	if err := disk.ValidateBase(imageInfo); err != nil {
		return "", Metadata{}, err
	}
	metadataPath, _ := s.metadataPath(digest)
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil || !metadataInfo.Mode().IsRegular() || metadataInfo.Mode()&os.ModeSymlink != 0 || metadataInfo.Mode().Perm() != 0o600 {
		return "", Metadata{}, errors.New("cached image metadata is not a mode-0600 regular non-symlink file")
	}
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return "", Metadata{}, err
	}
	var metadata Metadata
	decoder := json.NewDecoder(strings.NewReader(string(metadataBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return "", Metadata{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", Metadata{}, errors.New("cached image metadata has trailing JSON data")
	}
	if metadata.Schema != MetadataSchema || metadata.Digest != digest || metadata.ArtifactSize != size || metadata.VirtualSize != imageInfo.VirtualSize || metadata.Format != "qcow2" || metadata.Source == "" || metadata.ImportedAt.IsZero() {
		return "", Metadata{}, errors.New("cached image metadata does not match artifact")
	}
	return path, metadata, nil
}

func (s Store) publish(ctx context.Context, tempPath, digest, source string, artifactSize int64) (string, Metadata, error) {
	actual, size, err := digestFile(tempPath)
	if err != nil {
		return "", Metadata{}, err
	}
	if actual != digest || size != artifactSize {
		return "", Metadata{}, errors.New("copied/downloaded image digest or size changed before publish")
	}
	imageInfo, err := s.manager().Inspect(ctx, tempPath)
	if err != nil {
		return "", Metadata{}, err
	}
	if err := disk.ValidateBase(imageInfo); err != nil {
		return "", Metadata{}, err
	}
	if err := os.Chmod(tempPath, 0o444); err != nil {
		return "", Metadata{}, err
	}
	target, _ := s.Path(digest)
	if err := os.Rename(tempPath, target); err != nil {
		return "", Metadata{}, err
	}
	if err := fsutil.SyncDir(s.cacheDir()); err != nil {
		return "", Metadata{}, err
	}
	metadata := Metadata{Schema: MetadataSchema, Digest: digest, Format: "qcow2", VirtualSize: imageInfo.VirtualSize, ArtifactSize: artifactSize, Source: source, ImportedAt: time.Now().UTC()}
	metadataPath, _ := s.metadataPath(digest)
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", Metadata{}, err
	}
	if err := fsutil.AtomicWrite(metadataPath, append(data, '\n'), 0o600); err != nil {
		_ = os.Remove(target)
		_ = fsutil.SyncDir(s.cacheDir())
		return "", Metadata{}, err
	}
	return target, metadata, nil
}

func (s Store) Import(ctx context.Context, source, expectedDigest string) (string, Metadata, error) {
	if err := s.validate(); err != nil {
		return "", Metadata{}, err
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return "", Metadata{}, err
	}
	digest, sourceSize, err := digestFile(absSource)
	if err != nil {
		return "", Metadata{}, err
	}
	if expectedDigest != "" && digest != expectedDigest {
		return "", Metadata{}, fmt.Errorf("local image digest %s does not match expected %s", digest, expectedDigest)
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cacheLock, err := lock.Acquire(lockContext, s.lockPath(), false)
	if err != nil {
		return "", Metadata{}, err
	}
	defer cacheLock.Release()
	target, _ := s.Path(digest)
	if _, err := os.Lstat(target); err == nil {
		return s.ValidateCached(ctx, digest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", Metadata{}, err
	}
	tempPath, copied, err := fsutil.CopyToTemp(absSource, s.cacheDir(), ".import-*.partial", 0o600, MaxArtifactSize)
	if err != nil {
		return "", Metadata{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if copied != sourceSize {
		return "", Metadata{}, errors.New("local image changed while importing")
	}
	path, metadata, err := s.publish(ctx, tempPath, digest, "local:"+absSource, copied)
	if err == nil {
		keep = true
	}
	return path, metadata, err
}

func (s Store) httpClient() *http.Client {
	client := http.Client{Timeout: 30 * time.Minute}
	if s.HTTPClient != nil {
		client = *s.HTTPClient
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Minute
		}
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || request.URL.Scheme != "https" {
			return errors.New("image redirect is non-HTTPS or exceeds five hops")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	return &client
}

func (s Store) Pull(ctx context.Context, entry Entry) (string, Metadata, error) {
	if err := s.validate(); err != nil {
		return "", Metadata{}, err
	}
	if !digestPattern.MatchString(entry.SHA256) || entry.Format != "qcow2" {
		return "", Metadata{}, errors.New("manifest entry digest or format is invalid")
	}
	if entry.ArtifactSize < 0 || entry.ArtifactSize > MaxArtifactSize {
		return "", Metadata{}, errors.New("manifest entry artifact size is invalid")
	}
	parsed, err := url.Parse(entry.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", Metadata{}, errors.New("remote image URL must be absolute HTTPS")
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cacheLock, err := lock.Acquire(lockContext, s.lockPath(), false)
	if err != nil {
		return "", Metadata{}, err
	}
	defer cacheLock.Release()
	if target, metadata, err := s.ValidateCached(ctx, entry.SHA256); err == nil {
		return target, metadata, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(filepath.Join(s.cacheDir(), entry.SHA256+".qcow2")); statErr == nil {
			return "", Metadata{}, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.URL, nil)
	if err != nil {
		return "", Metadata{}, err
	}
	response, err := s.httpClient().Do(request)
	if err != nil {
		return "", Metadata{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", Metadata{}, fmt.Errorf("image download returned %s", response.Status)
	}
	if response.ContentLength > MaxArtifactSize {
		return "", Metadata{}, errors.New("image Content-Length exceeds limit")
	}
	if entry.ArtifactSize > 0 && response.ContentLength >= 0 && response.ContentLength != entry.ArtifactSize {
		return "", Metadata{}, errors.New("image Content-Length differs from manifest artifact size")
	}
	temp, err := os.CreateTemp(s.cacheDir(), ".download-*.partial")
	if err != nil {
		return "", Metadata{}, err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return "", Metadata{}, err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, MaxArtifactSize+1))
	if err != nil {
		return "", Metadata{}, fmt.Errorf("download image: %w", err)
	}
	if written > MaxArtifactSize {
		return "", Metadata{}, errors.New("download image exceeded size limit")
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return "", Metadata{}, errors.New("download size differs from Content-Length")
	}
	if entry.ArtifactSize > 0 && written != entry.ArtifactSize {
		return "", Metadata{}, errors.New("download size differs from manifest artifact size")
	}
	if digest := hex.EncodeToString(hash.Sum(nil)); digest != entry.SHA256 {
		return "", Metadata{}, fmt.Errorf("download digest %s does not match manifest %s", digest, entry.SHA256)
	}
	if err := temp.Sync(); err != nil {
		return "", Metadata{}, err
	}
	if err := temp.Close(); err != nil {
		return "", Metadata{}, err
	}
	path, metadata, err := s.publish(ctx, tempPath, entry.SHA256, "remote:"+entry.URL, written)
	if err == nil {
		keep = true
	}
	return path, metadata, err
}

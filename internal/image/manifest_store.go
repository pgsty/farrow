package image

import (
	"bytes"
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
	"strconv"
	"strings"
	"time"

	"aead.dev/minisign"
	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/webclient"
)

const (
	ManifestStateSchema = 1
	MaxSignatureSize    = 64 << 10
)

type ManifestState struct {
	Schema         int       `json:"schema"`
	HighestVersion uint64    `json:"highest_version"`
	Active         string    `json:"active"`
	ActiveVersion  uint64    `json:"active_version"`
	ActiveDigest   string    `json:"active_digest"`
	KeyID          string    `json:"key_id,omitempty"`
	Source         string    `json:"source"`
	AcceptedAt     time.Time `json:"accepted_at"`
	Downgrade      bool      `json:"downgrade"`
}

type ManifestManager struct {
	DataRoot   string
	Keys       []minisign.PublicKey
	HTTPClient *http.Client
}

var manifestFilename = regexp.MustCompile(`^v[0-9]+-[0-9a-f]{64}\.json$`)

func (m ManifestManager) keys() ([]minisign.PublicKey, error) {
	keys := m.Keys
	if len(keys) == 0 {
		keys = productionManifestKeys
	}
	if len(keys) < 2 {
		return nil, errors.New("manifest verifier has no active and standby production public keys")
	}
	return append([]minisign.PublicKey(nil), keys...), nil
}

func (m ManifestManager) root() string      { return filepath.Join(m.DataRoot, "images", "manifests") }
func (m ManifestManager) versions() string  { return filepath.Join(m.root(), "versions") }
func (m ManifestManager) statePath() string { return filepath.Join(m.root(), "state.json") }
func (m ManifestManager) lockPath() string {
	return filepath.Join(m.DataRoot, "locks", "manifest.lock")
}

func (m ManifestManager) validate() error {
	if m.DataRoot == "" || !filepath.IsAbs(m.DataRoot) {
		return errors.New("manifest data root must be absolute")
	}
	for _, directory := range []string{m.DataRoot, m.root(), m.versions(), filepath.Dir(m.lockPath())} {
		if err := ensurePrivateDir(directory); err != nil {
			return err
		}
	}
	return nil
}

func manifestDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func verifyManifest(keys []minisign.PublicKey, data, signatureText []byte) (uint64, error) {
	if len(signatureText) == 0 || len(signatureText) > MaxSignatureSize {
		return 0, errors.New("manifest signature size is invalid")
	}
	var signature minisign.Signature
	if err := signature.UnmarshalText(signatureText); err != nil {
		return 0, fmt.Errorf("decode minisign signature: %w", err)
	}
	for _, key := range keys {
		if key.ID() != signature.KeyID {
			continue
		}
		if !minisign.Verify(key, data, signatureText) {
			return 0, errors.New("minisign signature or trusted comment verification failed")
		}
		return key.ID(), nil
	}
	return 0, fmt.Errorf("manifest signature uses unknown key ID %016X", signature.KeyID)
}

func strictManifestState(data []byte) (ManifestState, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state ManifestState
	if err := decoder.Decode(&state); err != nil {
		return ManifestState{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ManifestState{}, errors.New("manifest state has trailing JSON")
	}
	if state.Schema != ManifestStateSchema || state.HighestVersion == 0 || state.ActiveVersion == 0 || state.HighestVersion < state.ActiveVersion || !digestPattern.MatchString(state.ActiveDigest) || state.Source == "" || state.AcceptedAt.IsZero() {
		return ManifestState{}, errors.New("manifest state fields are invalid")
	}
	if state.Active != "embedded" && !manifestFilename.MatchString(state.Active) {
		return ManifestState{}, errors.New("manifest state active filename is unsafe")
	}
	return state, nil
}

func (m ManifestManager) readState() (ManifestState, error) {
	info, err := os.Lstat(m.statePath())
	if err != nil {
		return ManifestState{}, err
	}
	if !info.Mode().IsRegular() {
		return ManifestState{}, errors.New("manifest state must be a regular non-symlink file")
	}
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		return ManifestState{}, err
	}
	return strictManifestState(data)
}

func embeddedState() (ManifestState, error) {
	data, err := EmbeddedCatalogBytes()
	if err != nil {
		return ManifestState{}, err
	}
	return ManifestState{Schema: ManifestStateSchema, HighestVersion: EmbeddedManifestVersion, Active: "embedded", ActiveVersion: EmbeddedManifestVersion, ActiveDigest: manifestDigest(data), Source: "embedded", AcceptedAt: embeddedManifestGeneratedAt}, nil
}

// currentBaselineState upgrades a prior binary's embedded pointer in memory.
// A signed active manifest is never silently replaced; reset remains the
// explicit recovery path when its high-water mark predates this binary.
func currentBaselineState(state ManifestState) (ManifestState, error) {
	if state.HighestVersion < EmbeddedManifestVersion {
		if state.Active != "embedded" || state.Source != "embedded" {
			return ManifestState{}, errors.New("active manifest state predates the embedded baseline; explicit reset is required")
		}
		return embeddedState()
	}
	if state.Active != "embedded" {
		return state, nil
	}
	data, err := EmbeddedCatalogBytes()
	if err != nil {
		return ManifestState{}, err
	}
	if state.ActiveVersion != EmbeddedManifestVersion || state.ActiveDigest != manifestDigest(data) {
		return ManifestState{}, errors.New("embedded manifest state does not match binary baseline")
	}
	return state, nil
}

func (m ManifestManager) Current() (Catalog, ManifestState, error) {
	if m.DataRoot == "" || !filepath.IsAbs(m.DataRoot) {
		return Catalog{}, ManifestState{}, errors.New("manifest data root must be absolute")
	}
	state, err := m.readState()
	if errors.Is(err, os.ErrNotExist) {
		state, err = embeddedState()
		if err != nil {
			return Catalog{}, ManifestState{}, err
		}
		return EmbeddedCatalog(), state, nil
	}
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	state, err = currentBaselineState(state)
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	if state.Active == "embedded" {
		return EmbeddedCatalog(), state, nil
	}
	keys, err := m.keys()
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	manifestPath := filepath.Join(m.versions(), state.Active)
	signaturePath := manifestPath + ".minisig"
	data, err := readLimitedFile(manifestPath, MaxManifestSize)
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	signatureText, err := readLimitedFile(signaturePath, MaxSignatureSize)
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	keyID, err := verifyManifest(keys, data, signatureText)
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	catalog, err := strictCatalog(data)
	if err != nil {
		return Catalog{}, ManifestState{}, err
	}
	if catalog.Version != state.ActiveVersion || manifestDigest(data) != state.ActiveDigest || strings.ToUpper(strconv.FormatUint(keyID, 16)) != state.KeyID {
		return Catalog{}, ManifestState{}, errors.New("active manifest bytes/signature do not match state pointer")
	}
	return catalog, state, nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("source must be a regular file no larger than %d bytes: %s", limit, path)
	}
	return os.ReadFile(path)
}

func (m ManifestManager) httpClient() *http.Client {
	client := *webclient.New(30 * time.Second)
	if m.HTTPClient != nil {
		client = *m.HTTPClient
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Second
		}
	}
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || (request.URL.Scheme != "http" && request.URL.Scheme != "https") {
			return errors.New("catalog redirect scheme is invalid or exceeds five hops")
		}
		if len(via) > 0 && via[0].URL.Scheme == "https" && request.URL.Scheme != "https" {
			return errors.New("catalog redirect attempts an HTTPS downgrade")
		}
		if previous != nil {
			return previous(request, via)
		}
		return nil
	}
	return &client
}

func (m ManifestManager) download(ctx context.Context, source string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := m.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > limit {
		return nil, fmt.Errorf("manifest download failed status/size policy: %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("manifest download exceeds limit or failed")
	}
	return data, nil
}

func (m ManifestManager) readSource(ctx context.Context, source string) ([]byte, []byte, string, error) {
	parsed, err := url.Parse(source)
	if err == nil && parsed.Scheme != "" {
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, nil, "", errors.New("remote catalog source must be absolute HTTP(S) without credentials, query, or fragment")
		}
		manifestSource := parsed.String()
		signatureURL := *parsed
		signatureURL.Path += ".minisig"
		signatureURL.RawPath = ""
		signatureSource := signatureURL.String()
		data, err := m.download(ctx, manifestSource, MaxManifestSize)
		if err != nil {
			return nil, nil, "", err
		}
		signatureText, err := m.download(ctx, signatureSource, MaxSignatureSize)
		return data, signatureText, manifestSource, err
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return nil, nil, "", err
	}
	data, err := readLimitedFile(absolute, MaxManifestSize)
	if err != nil {
		return nil, nil, "", err
	}
	signatureText, err := readLimitedFile(absolute+".minisig", MaxSignatureSize)
	return data, signatureText, "local:" + absolute, err
}

func (m ManifestManager) Sync(ctx context.Context, source string, allowDowngrade bool) (ManifestState, error) {
	if err := m.validate(); err != nil {
		return ManifestState{}, err
	}
	keys, err := m.keys()
	if err != nil {
		return ManifestState{}, err
	}
	data, signatureText, provenance, err := m.readSource(ctx, source)
	if err != nil {
		return ManifestState{}, err
	}
	keyID, err := verifyManifest(keys, data, signatureText)
	if err != nil {
		return ManifestState{}, err
	}
	catalog, err := strictCatalog(data)
	if err != nil {
		return ManifestState{}, err
	}
	digest := manifestDigest(data)
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manifestLock, err := lock.Acquire(lockContext, m.lockPath(), false)
	if err != nil {
		return ManifestState{}, err
	}
	defer manifestLock.Release()
	current, err := m.readState()
	if errors.Is(err, os.ErrNotExist) {
		current, err = embeddedState()
	}
	if err != nil {
		return ManifestState{}, err
	}
	current, err = currentBaselineState(current)
	if err != nil {
		return ManifestState{}, err
	}
	if catalog.Version == current.ActiveVersion && digest != current.ActiveDigest {
		return ManifestState{}, errors.New("manifest version equivocation: same version has different bytes")
	}
	if catalog.Version < current.HighestVersion && !allowDowngrade {
		return ManifestState{}, fmt.Errorf("manifest version %d is below accepted high-water mark %d", catalog.Version, current.HighestVersion)
	}
	filename := fmt.Sprintf("v%d-%s.json", catalog.Version, digest)
	manifestPath := filepath.Join(m.versions(), filename)
	if existing, err := os.ReadFile(manifestPath); err == nil {
		if !bytes.Equal(existing, data) {
			return ManifestState{}, errors.New("versioned manifest path contains different bytes")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := fsutil.AtomicWrite(manifestPath, data, 0o600); err != nil {
			return ManifestState{}, err
		}
	} else {
		return ManifestState{}, err
	}
	signaturePath := manifestPath + ".minisig"
	if existing, err := os.ReadFile(signaturePath); err == nil {
		if !bytes.Equal(existing, signatureText) {
			return ManifestState{}, errors.New("versioned signature path contains different bytes")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := fsutil.AtomicWrite(signaturePath, signatureText, 0o600); err != nil {
			return ManifestState{}, err
		}
	} else {
		return ManifestState{}, err
	}
	highest := current.HighestVersion
	if catalog.Version > highest {
		highest = catalog.Version
	}
	state := ManifestState{Schema: ManifestStateSchema, HighestVersion: highest, Active: filename, ActiveVersion: catalog.Version, ActiveDigest: digest, KeyID: strings.ToUpper(strconv.FormatUint(keyID, 16)), Source: provenance, AcceptedAt: time.Now().UTC(), Downgrade: catalog.Version < current.HighestVersion}
	stateBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return ManifestState{}, err
	}
	if err := fsutil.AtomicWrite(m.statePath(), append(stateBytes, '\n'), 0o600); err != nil {
		return ManifestState{}, err
	}
	return state, nil
}

func (m ManifestManager) Reset(ctx context.Context) (ManifestState, error) {
	if err := m.validate(); err != nil {
		return ManifestState{}, err
	}
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manifestLock, err := lock.Acquire(lockContext, m.lockPath(), false)
	if err != nil {
		return ManifestState{}, err
	}
	defer manifestLock.Release()
	current, err := m.readState()
	if errors.Is(err, os.ErrNotExist) {
		current, err = embeddedState()
	}
	if err != nil {
		return ManifestState{}, err
	}
	embedded, err := embeddedState()
	if err != nil {
		return ManifestState{}, err
	}
	if current.HighestVersion > embedded.HighestVersion {
		embedded.HighestVersion = current.HighestVersion
	}
	embedded.AcceptedAt = time.Now().UTC()
	data, err := json.MarshalIndent(embedded, "", "  ")
	if err != nil {
		return ManifestState{}, err
	}
	if err := fsutil.AtomicWrite(m.statePath(), append(data, '\n'), 0o600); err != nil {
		return ManifestState{}, err
	}
	return embedded, nil
}

package pigsty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pgsty/farrow/internal/fsutil"
)

var (
	ErrOutputConflict  = errors.New("managed inventory output conflicts with requested content")
	ErrOutputIntegrity = errors.New("inventory output ownership or integrity check failed")
)

type outputManifest struct {
	Schema        int    `json:"schema"`
	OutputPath    string `json:"output_path"`
	OutputSHA256  string `json:"output_sha256"`
	SourcePath    string `json:"source_path"`
	SourceSHA256  string `json:"source_sha256"`
	Profile       string `json:"profile"`
	Scale         int    `json:"scale"`
	NetworkCIDR   string `json:"network_cidr"`
	InventoryMode string `json:"inventory_mode"`
}

type PublishResult struct {
	Path       string
	MarkerPath string
	Changed    bool
}

func markerPath(output string) string { return output + ".farrow.json" }

// Publish writes a mode-0600 inventory and ownership manifest. Existing
// output is replaceable only when its strict marker proves Farrow ownership
// and its current digest still matches that marker.
func Publish(output string, rendered Result, force bool) (PublishResult, error) {
	if output == "" || !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return PublishResult{}, fmt.Errorf("%w: output must be a clean absolute path", ErrOutputIntegrity)
	}
	if output == rendered.SourcePath {
		return PublishResult{}, fmt.Errorf("%w: refuse to overwrite the bound Pigsty source inventory", ErrOutputIntegrity)
	}
	marker := markerPath(output)
	wanted := outputManifest{
		Schema: 2, OutputPath: output, OutputSHA256: rendered.OutputSHA256,
		SourcePath: rendered.SourcePath, SourceSHA256: rendered.SourceSHA256,
		Profile: rendered.Profile, Scale: rendered.Scale, NetworkCIDR: rendered.TargetCIDR,
		InventoryMode: string(rendered.InventoryMode),
	}
	markerData, err := json.MarshalIndent(wanted, "", "  ")
	if err != nil {
		return PublishResult{}, err
	}
	markerData = append(markerData, '\n')

	info, statErr := os.Lstat(output)
	if errors.Is(statErr, os.ErrNotExist) {
		if _, markerErr := os.Lstat(marker); !errors.Is(markerErr, os.ErrNotExist) {
			return PublishResult{}, fmt.Errorf("%w: marker exists without its inventory: %s", ErrOutputIntegrity, marker)
		}
		if err := fsutil.AtomicWrite(output, rendered.Data, 0o600); err != nil {
			return PublishResult{}, fmt.Errorf("%w: %v", ErrOutputIntegrity, err)
		}
		if err := fsutil.AtomicWrite(marker, markerData, 0o600); err != nil {
			return PublishResult{}, fmt.Errorf("%w: inventory was written but marker publication failed: %v", ErrOutputIntegrity, err)
		}
		return PublishResult{Path: output, MarkerPath: marker, Changed: true}, nil
	}
	if statErr != nil {
		return PublishResult{}, fmt.Errorf("%w: %v", ErrOutputIntegrity, statErr)
	}
	if err := ownedRegular(info, 0o600); err != nil {
		return PublishResult{}, fmt.Errorf("%w: output %s: %v", ErrOutputIntegrity, output, err)
	}
	current, err := os.ReadFile(output)
	if err != nil || len(current) > maxInventoryBytes {
		return PublishResult{}, fmt.Errorf("%w: cannot read bounded output", ErrOutputIntegrity)
	}
	previous, err := loadManifest(marker)
	if err != nil {
		return PublishResult{}, err
	}
	if previous.OutputPath != output || digestBytes(current) != previous.OutputSHA256 {
		return PublishResult{}, fmt.Errorf("%w: output differs from its ownership marker", ErrOutputIntegrity)
	}
	if bytes.Equal(current, rendered.Data) && previous == wanted {
		return PublishResult{Path: output, MarkerPath: marker}, nil
	}
	if !force {
		return PublishResult{}, fmt.Errorf("%w: review the managed output and pass --force for an atomic replacement", ErrOutputConflict)
	}
	if err := fsutil.AtomicWrite(output, rendered.Data, 0o600); err != nil {
		return PublishResult{}, fmt.Errorf("%w: %v", ErrOutputIntegrity, err)
	}
	if err := fsutil.AtomicWrite(marker, markerData, 0o600); err != nil {
		return PublishResult{}, fmt.Errorf("%w: output changed but marker publication failed: %v", ErrOutputIntegrity, err)
	}
	return PublishResult{Path: output, MarkerPath: marker, Changed: true}, nil
}

func loadManifest(path string) (outputManifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return outputManifest{}, fmt.Errorf("%w: managed marker is missing: %s", ErrOutputIntegrity, path)
	}
	if err := ownedRegular(info, 0o600); err != nil || info.Size() <= 0 || info.Size() > 4096 {
		return outputManifest{}, fmt.Errorf("%w: marker %s is unsafe", ErrOutputIntegrity, path)
	}
	handle, err := os.Open(path)
	if err != nil {
		return outputManifest{}, fmt.Errorf("%w: %v", ErrOutputIntegrity, err)
	}
	defer handle.Close()
	decoder := json.NewDecoder(io.LimitReader(handle, 4097))
	decoder.DisallowUnknownFields()
	var marker outputManifest
	if err := decoder.Decode(&marker); err != nil {
		return outputManifest{}, fmt.Errorf("%w: invalid marker: %v", ErrOutputIntegrity, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return outputManifest{}, fmt.Errorf("%w: marker has trailing data", ErrOutputIntegrity)
	}
	if marker.Schema == 1 && marker.Scale == 0 {
		marker.Scale = 1
	}
	if (marker.Schema != 1 && marker.Schema != 2) || !filepath.IsAbs(marker.OutputPath) || !filepath.IsAbs(marker.SourcePath) || len(marker.OutputSHA256) != 64 || len(marker.SourceSHA256) != 64 || marker.Profile == "" || marker.Scale < 1 || marker.Scale > 64 || marker.NetworkCIDR == "" || marker.InventoryMode == "" {
		return outputManifest{}, fmt.Errorf("%w: marker contract is incomplete", ErrOutputIntegrity)
	}
	return marker, nil
}

func ownedRegular(info os.FileInfo, mode os.FileMode) error {
	statistics, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(statistics.Uid) != os.Geteuid() || statistics.Nlink != 1 || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
		return errors.New("expected current-user regular file with one link and exact mode")
	}
	return nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

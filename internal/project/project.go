// Package project owns the small workspace marker and the mapping to the
// versioned user data root.
package project

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
)

const MarkerSchema = 1

var ErrDataRootMigrationRequired = errors.New("project data-root migration required")

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	nodePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

type Marker struct {
	Schema    int       `json:"schema"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
	DataRoot  string    `json:"data_root"`
}

type Project struct {
	WorkDir    string
	MarkerDir  string
	MarkerPath string
	DataRoot   string
	Root       string
	Marker     Marker
}

type Environment interface {
	Getenv(string) string
	UserHomeDir() (string, error)
}

type osEnvironment struct{}

func (osEnvironment) Getenv(key string) string     { return os.Getenv(key) }
func (osEnvironment) UserHomeDir() (string, error) { return os.UserHomeDir() }

func canonicalWorkDir(cwd string) (string, error) {
	if cwd == "" {
		return "", errors.New("project working directory is empty")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project working directory is not a directory: %s", canonical)
	}
	return canonical, nil
}

func unsafeRoot(root, home, cwd, xdgRoot string) bool {
	clean := filepath.Clean(root)
	for _, unsafe := range []string{"/", filepath.Clean(home), filepath.Clean(cwd), filepath.Clean(xdgRoot)} {
		if unsafe != "." && unsafe != "" && clean == unsafe {
			return true
		}
	}
	return false
}

func canonicalIfExisting(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if canonical, err := filepath.EvalSymlinks(abs); err == nil {
		return canonical
	}
	return abs
}

// ResolveDataRoot implements FARROW_DATA_HOME, XDG_DATA_HOME/farrow, then a
// stable per-user fallback. It does not create the directory.
func ResolveDataRoot(cwd string, environment Environment) (string, error) {
	return ResolveDataRootWithConfig(cwd, "", environment)
}

// ResolveDataRootWithConfig implements the complete project-creation
// precedence: FARROW_DATA_HOME, storage.data_root, XDG_DATA_HOME/farrow, then
// the stable per-user fallback. It does not create the directory.
func ResolveDataRootWithConfig(cwd, configured string, environment Environment) (string, error) {
	if environment == nil {
		environment = osEnvironment{}
	}
	if configured != "" && (!filepath.IsAbs(configured) || filepath.Clean(configured) != configured || configured == "/") {
		return "", errors.New("storage.data_root must be a clean non-root absolute path")
	}
	workDir, err := canonicalWorkDir(cwd)
	if err != nil {
		return "", err
	}
	home, err := environment.UserHomeDir()
	if err != nil {
		return "", err
	}
	home = canonicalIfExisting(home)
	xdgRoot := environment.Getenv("XDG_DATA_HOME")
	if xdgRoot != "" && !filepath.IsAbs(xdgRoot) {
		return "", errors.New("XDG_DATA_HOME must be absolute")
	}
	root := environment.Getenv("FARROW_DATA_HOME")
	if root == "" {
		root = configured
	}
	if root == "" {
		if xdgRoot != "" {
			root = filepath.Join(xdgRoot, "farrow")
		} else if runtime.GOOS == "darwin" {
			root = filepath.Join(home, "Library", "Application Support", "farrow")
		} else {
			root = filepath.Join(home, ".local", "share", "farrow")
		}
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("farrow data root must be absolute")
	}
	root = filepath.Clean(root)
	comparisonRoot := canonicalIfExisting(root)
	if unsafeRoot(comparisonRoot, home, workDir, canonicalIfExisting(xdgRoot)) {
		return "", fmt.Errorf("unsafe broad Farrow data root: %s", root)
	}
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("farrow data root must not be a symlink: %s", root)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return root, nil
}

func NewUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func ValidUUID(value string) bool { return uuidPattern.MatchString(value) }

func decodeMarker(path string) (Marker, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Marker{}, err
	}
	if !info.Mode().IsRegular() {
		return Marker{}, errors.New("project marker must be a regular non-symlink file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Marker{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker Marker
	if err := decoder.Decode(&marker); err != nil {
		return Marker{}, fmt.Errorf("decode project marker: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Marker{}, errors.New("project marker contains trailing JSON data")
	}
	if marker.Schema != MarkerSchema || !uuidPattern.MatchString(marker.ProjectID) || !filepath.IsAbs(marker.DataRoot) || marker.CreatedAt.IsZero() {
		return Marker{}, errors.New("project marker fields are invalid or unsupported")
	}
	return marker, nil
}

func paths(workDir string, marker Marker) Project {
	markerDir := filepath.Join(workDir, ".farrow")
	return Project{
		WorkDir: workDir, MarkerDir: markerDir, MarkerPath: filepath.Join(markerDir, "project.json"),
		DataRoot: marker.DataRoot, Root: filepath.Join(marker.DataRoot, "projects", marker.ProjectID), Marker: marker,
	}
}

func ensureDir(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("required path is not a real directory: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("required directory is group/world writable: %s mode=%o", path, info.Mode().Perm())
	}
	return os.Chmod(path, mode)
}

func Create(cwd, dataRoot string) (Project, error) {
	workDir, err := canonicalWorkDir(cwd)
	if err != nil {
		return Project{}, err
	}
	dataRoot = canonicalIfExisting(dataRoot)
	if !filepath.IsAbs(dataRoot) || unsafeRoot(dataRoot, "", workDir, "") {
		return Project{}, errors.New("project data root is unsafe or not absolute")
	}
	markerDir := filepath.Join(workDir, ".farrow")
	markerPath := filepath.Join(markerDir, "project.json")
	if _, err := os.Lstat(markerPath); err == nil {
		return Open(workDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Project{}, err
	}
	for _, directory := range []string{dataRoot, filepath.Join(dataRoot, "projects"), filepath.Join(dataRoot, "cache"), filepath.Join(dataRoot, "locks"), markerDir} {
		if err := ensureDir(directory, 0o700); err != nil {
			return Project{}, err
		}
	}
	projectID, err := NewUUID()
	if err != nil {
		return Project{}, err
	}
	marker := Marker{Schema: MarkerSchema, ProjectID: projectID, CreatedAt: time.Now().UTC(), DataRoot: filepath.Clean(dataRoot)}
	project := paths(workDir, marker)
	if err := ensureDir(project.Root, 0o700); err != nil {
		return Project{}, err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return Project{}, err
	}
	data = append(data, '\n')
	if err := fsutil.AtomicWrite(filepath.Join(project.Root, "project.json"), data, 0o600); err != nil {
		return Project{}, err
	}
	if err := fsutil.AtomicWrite(markerPath, data, 0o600); err != nil {
		return Project{}, err
	}
	return project, nil
}

func Open(cwd string) (Project, error) {
	workDir, err := canonicalWorkDir(cwd)
	if err != nil {
		return Project{}, err
	}
	markerPath := filepath.Join(workDir, ".farrow", "project.json")
	marker, err := decodeMarker(markerPath)
	if err != nil {
		return Project{}, err
	}
	project := paths(workDir, marker)
	inside, err := fsutil.IsWithin(filepath.Join(marker.DataRoot, "projects"), project.Root)
	if err != nil || !inside {
		return Project{}, errors.New("project state root escapes data root")
	}
	rootInfo, err := os.Lstat(project.Root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || rootInfo.Mode().Perm()&0o022 != 0 {
		return Project{}, errors.New("project state root is missing, unsafe, or writable by other users")
	}
	dataMarker, err := decodeMarker(filepath.Join(project.Root, "project.json"))
	if err != nil || dataMarker != marker {
		return Project{}, errors.New("workspace and data-root project markers do not match")
	}
	return project, nil
}

func (p Project) NodeDir(name string) (string, error) {
	if !nodePattern.MatchString(name) {
		return "", fmt.Errorf("invalid node name %q", name)
	}
	return filepath.Join(p.Root, "nodes", name), nil
}

func (p Project) EnsureNodeDir(name string) (string, error) {
	directory, err := p.NodeDir(name)
	if err != nil {
		return "", err
	}
	if err := ensureDir(filepath.Dir(directory), 0o700); err != nil {
		return "", err
	}
	if err := ensureDir(directory, 0o700); err != nil {
		return "", err
	}
	return directory, nil
}

func (p Project) RuntimeID() string {
	return strings.ReplaceAll(p.Marker.ProjectID[:8], "-", "")
}

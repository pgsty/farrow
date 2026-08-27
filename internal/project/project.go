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
	"strings"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
)

// MarkerSchema 2 adds work_dir and name so a data-root project can be traced
// back to its workspace directory (orphan detection, list, prune). Schema-1
// markers stay readable; `farrow project upgrade-state` rewrites them.
const MarkerSchema = 2

var ErrDataRootMigrationRequired = errors.New("project data-root migration required")

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	nodePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type Marker struct {
	Schema    int       `json:"schema"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
	DataRoot  string    `json:"data_root"`
	WorkDir   string    `json:"work_dir,omitempty"`
	Name      string    `json:"name,omitempty"`
}

// SameIdentity reports whether two markers describe the same project. The
// schema and the schema-2 convenience fields may differ transiently while a
// marker pair is being upgraded; project identity may not.
func (m Marker) SameIdentity(other Marker) bool {
	return m.ProjectID == other.ProjectID && m.DataRoot == other.DataRoot && m.CreatedAt.Equal(other.CreatedAt)
}

// MarkerName returns a display-safe project name for a workspace directory,
// or "" when the directory basename is not a safe name.
func MarkerName(workDir string) string {
	base := filepath.Base(workDir)
	if namePattern.MatchString(base) {
		return base
	}
	return ""
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

func unsafeRoot(root, home, cwd string) bool {
	clean := filepath.Clean(root)
	for _, unsafe := range []string{"/", filepath.Clean(home), filepath.Clean(cwd)} {
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

// canonicalWithMissing resolves every existing ancestor and then restores the
// not-yet-created suffix. EvalSymlinks alone cannot inspect a symlinked parent
// when the final data-root directory does not exist yet.
func canonicalWithMissing(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	probe := abs
	missing := make([]string, 0)
	for {
		canonical, evalErr := filepath.EvalSymlinks(probe)
		if evalErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, missing[index])
			}
			return canonical, nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", evalErr
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", evalErr
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

// ResolveDataRoot implements FARROW_HOME, then ~/.farrow. It does not create
// the directory.
func ResolveDataRoot(cwd string, environment Environment) (string, error) {
	return ResolveDataRootWithConfig(cwd, "", environment)
}

// ResolveDataRootWithConfig implements the complete project-creation
// precedence: FARROW_HOME, storage.data_root, then ~/.farrow on both Linux and
// macOS. It does not create the directory.
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
	root := environment.Getenv("FARROW_HOME")
	if root == "" {
		root = configured
	}
	if root == "" {
		root = filepath.Join(home, ".farrow")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("farrow data root must be absolute")
	}
	root = filepath.Clean(root)
	comparisonRoot, err := canonicalWithMissing(root)
	if err != nil {
		return "", err
	}
	if unsafeRoot(comparisonRoot, home, workDir) {
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
	if (marker.Schema != 1 && marker.Schema != MarkerSchema) || !uuidPattern.MatchString(marker.ProjectID) || !filepath.IsAbs(marker.DataRoot) || marker.CreatedAt.IsZero() {
		return Marker{}, errors.New("project marker fields are invalid or unsupported")
	}
	if marker.Schema == 1 && (marker.WorkDir != "" || marker.Name != "") {
		return Marker{}, errors.New("schema-1 project marker unexpectedly carries schema-2 fields")
	}
	if marker.Schema == MarkerSchema && (!filepath.IsAbs(marker.WorkDir) || (marker.Name != "" && !namePattern.MatchString(marker.Name))) {
		return Marker{}, errors.New("project marker work_dir or name is invalid")
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
	if !filepath.IsAbs(dataRoot) {
		return Project{}, errors.New("project data root is unsafe or not absolute")
	}
	dataRoot = filepath.Clean(dataRoot)
	if info, statErr := os.Lstat(dataRoot); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return Project{}, fmt.Errorf("project data root must not be a symlink: %s", dataRoot)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Project{}, statErr
	}
	comparisonRoot, err := canonicalWithMissing(dataRoot)
	if err != nil || unsafeRoot(comparisonRoot, "", workDir) {
		return Project{}, errors.New("project data root is unsafe or not absolute")
	}
	markerDir := filepath.Join(workDir, ".farrow")
	markerPath := filepath.Join(markerDir, "project.json")
	if _, err := os.Lstat(markerPath); err == nil {
		return Open(workDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Project{}, err
	}
	for _, directory := range []string{dataRoot, filepath.Join(dataRoot, "projects"), filepath.Join(dataRoot, "locks"), markerDir} {
		if err := ensureDir(directory, 0o700); err != nil {
			return Project{}, err
		}
	}
	projectID, err := NewUUID()
	if err != nil {
		return Project{}, err
	}
	marker := Marker{Schema: MarkerSchema, ProjectID: projectID, CreatedAt: time.Now().UTC(), DataRoot: filepath.Clean(dataRoot), WorkDir: workDir, Name: MarkerName(workDir)}
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
	// The marker records the machine-local data root; the workspace directory
	// is often a git checkout, so keep the whole .farrow directory out of it.
	if err := fsutil.AtomicWrite(filepath.Join(markerDir, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		return Project{}, err
	}
	return project, nil
}

// OpenConfigured opens the current project and enforces any explicitly
// selected data root. When create is true, a missing project is created using
// the normal FARROW_HOME/config/default precedence.
func OpenConfigured(cwd, configuredDataRoot string, create bool) (Project, error) {
	opened, err := Open(cwd)
	if err == nil {
		if configuredDataRoot != "" || os.Getenv("FARROW_HOME") != "" {
			selected, selectErr := ResolveDataRootWithConfig(cwd, configuredDataRoot, nil)
			if selectErr != nil {
				return Project{}, selectErr
			}
			if filepath.Clean(selected) != filepath.Clean(opened.DataRoot) {
				return Project{}, fmt.Errorf("%w: project is already rooted at %s, requested %s", ErrDataRootMigrationRequired, opened.DataRoot, selected)
			}
		}
		return opened, nil
	}
	if !create {
		return opened, err
	}
	var pathError *os.PathError
	if !errors.Is(err, os.ErrNotExist) && (!errors.As(err, &pathError) || !errors.Is(pathError.Err, os.ErrNotExist)) {
		return Project{}, err
	}
	dataRoot, err := ResolveDataRootWithConfig(cwd, configuredDataRoot, nil)
	if err != nil {
		return Project{}, err
	}
	return Create(cwd, dataRoot)
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
	if err != nil || !dataMarker.SameIdentity(marker) {
		return Project{}, errors.New("workspace and data-root project markers do not match")
	}
	return project, nil
}

// UpgradeMarkers rewrites both marker copies at the current schema, recording
// the workspace directory and its display name. The data-root copy is written
// first so a crash between the two writes still leaves the pair openable.
func (p Project) UpgradeMarkers() (bool, error) {
	if p.WorkDir == "" || p.Root == "" {
		return false, errors.New("marker upgrade requires an opened project")
	}
	upgraded := p.Marker
	upgraded.Schema = MarkerSchema
	upgraded.WorkDir = p.WorkDir
	upgraded.Name = MarkerName(p.WorkDir)
	if upgraded == p.Marker {
		return false, nil
	}
	data, err := json.MarshalIndent(upgraded, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	for _, path := range []string{filepath.Join(p.Root, "project.json"), p.MarkerPath} {
		if err := fsutil.AtomicWrite(path, data, 0o600); err != nil {
			return false, err
		}
	}
	return true, nil
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

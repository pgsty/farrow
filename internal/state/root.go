package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
// not-yet-created suffix, so a symlinked parent of a missing data root is
// still inspected.
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

func unsafeRoot(root, home, cwd string) bool {
	clean := filepath.Clean(root)
	for _, unsafe := range []string{"/", filepath.Clean(home), filepath.Clean(cwd)} {
		if unsafe != "." && unsafe != "" && clean == unsafe {
			return true
		}
	}
	return false
}

// ResolveDataRoot implements the complete precedence: FARROW_HOME, then
// ~/.farrow. It refuses broad or symlinked roots and does not create the
// directory. A pre-simplification layout (a projects/ registry) is a hard
// error with its one-line migration path.
func ResolveDataRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	home = canonicalIfExisting(home)
	cwd, _ := os.Getwd()
	root := os.Getenv("FARROW_HOME")
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
	if unsafeRoot(comparisonRoot, home, canonicalIfExisting(cwd)) {
		return "", fmt.Errorf("unsafe broad Farrow data root: %s", root)
	}
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("farrow data root must not be a symlink: %s", root)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := os.Lstat(filepath.Join(root, "projects")); err == nil {
		return "", fmt.Errorf("%s holds a pre-simplification multi-project layout; farrow now keeps exactly one deployment there — remove it with `rm -rf %s` (images are re-pulled on demand) and run setup/up again", root, root)
	}
	return root, nil
}

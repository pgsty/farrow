package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Discovery struct {
	DataRoot string
	Projects []Project
	Warnings []string
}

// Discover enumerates only marker-verified project roots directly below one
// resolved user data root. Unsafe or malformed entries are reported and never
// traversed.
func Discover(dataRoot string) (Discovery, error) {
	if dataRoot == "" || !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) == "/" {
		return Discovery{}, errors.New("discovery data root must be a non-root absolute path")
	}
	dataRoot = filepath.Clean(dataRoot)
	result := Discovery{DataRoot: dataRoot}
	rootInfo, err := os.Lstat(dataRoot)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return Discovery{}, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || rootInfo.Mode().Perm()&0o022 != 0 {
		return Discovery{}, errors.New("discovery data root is unsafe or writable by other users")
	}
	projectsDir := filepath.Join(dataRoot, "projects")
	projectsInfo, err := os.Lstat(projectsDir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return Discovery{}, err
	}
	if !projectsInfo.IsDir() || projectsInfo.Mode()&os.ModeSymlink != 0 || projectsInfo.Mode().Perm()&0o022 != 0 {
		return Discovery{}, errors.New("projects registry is unsafe or writable by other users")
	}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return Discovery{}, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !uuidPattern.MatchString(name) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("ignored non-project registry entry %q", name))
			continue
		}
		root := filepath.Join(projectsDir, name)
		info, statErr := os.Lstat(root)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("ignored unsafe project root %q", name))
			continue
		}
		marker, markerErr := decodeMarker(filepath.Join(root, "project.json"))
		if markerErr != nil || marker.ProjectID != name || canonicalIfExisting(marker.DataRoot) != canonicalIfExisting(dataRoot) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("ignored project %q with invalid registry marker", name))
			continue
		}
		result.Projects = append(result.Projects, Project{DataRoot: dataRoot, Root: root, Marker: marker})
	}
	sort.Slice(result.Projects, func(i, j int) bool { return result.Projects[i].Marker.ProjectID < result.Projects[j].Marker.ProjectID })
	sort.Strings(result.Warnings)
	return result, nil
}

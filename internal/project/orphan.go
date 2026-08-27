package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// OrphanState classifies one registered project against its recorded
// workspace: "workdir-missing" (directory deleted), "workdir-mismatch"
// (directory reused by another project), or "unknown-workdir" (schema-1
// marker predating work_dir). The current project is never an orphan.
func OrphanState(marker Marker, current bool) string {
	if current {
		return ""
	}
	if marker.WorkDir == "" {
		return "unknown-workdir"
	}
	workspaceMarker := filepath.Join(marker.WorkDir, ".farrow", "project.json")
	data, err := os.ReadFile(workspaceMarker)
	if errors.Is(err, os.ErrNotExist) {
		return "workdir-missing"
	}
	if err != nil {
		return "workdir-mismatch"
	}
	var decoded Marker
	if decodeErr := json.Unmarshal(data, &decoded); decodeErr != nil || !decoded.SameIdentity(marker) {
		return "workdir-mismatch"
	}
	return ""
}

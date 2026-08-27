package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pgsty/farrow/internal/fsutil"
	privatevm "github.com/pgsty/farrow/internal/private"
	"github.com/pgsty/farrow/internal/project"
	"github.com/pgsty/farrow/internal/quick"
	"github.com/pgsty/farrow/internal/state"
	"github.com/pgsty/farrow/internal/version"
)

// Data-root-side project administration: removing a registered project whose
// workspace directory is gone (or being purged deliberately), and sweeping
// orphans. The removal path deliberately reuses the ordinary lifecycle: a
// throwaway workspace is materialized from the registered marker, so destroy,
// persistent-disk deletion, and key purge run with every existing safety
// boundary intact.

var errProjectResidue = errors.New("project directory holds unexpected artifacts")

func findRegisteredProject(dataRoot, projectID string) (project.Project, error) {
	discovery, err := project.Discover(dataRoot)
	if err != nil {
		return project.Project{}, err
	}
	for _, candidate := range discovery.Projects {
		if candidate.Marker.ProjectID == projectID {
			return candidate, nil
		}
	}
	return project.Project{}, fmt.Errorf("data root %s has no registered project %s; run `farrow list --json`", dataRoot, projectID)
}

// materializeRescueWorkspace writes the registered marker into a fresh
// throwaway directory so the ordinary cwd-bound lifecycle can operate on the
// project. The caller removes the directory.
func materializeRescueWorkspace(registered project.Project) (string, error) {
	rescue, err := os.MkdirTemp("", "farrow-project-rm-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(rescue, 0o700); err != nil {
		_ = os.RemoveAll(rescue)
		return "", err
	}
	markerData, err := os.ReadFile(filepath.Join(registered.Root, "project.json"))
	if err != nil {
		_ = os.RemoveAll(rescue)
		return "", err
	}
	if err := os.Mkdir(filepath.Join(rescue, ".farrow"), 0o700); err != nil {
		_ = os.RemoveAll(rescue)
		return "", err
	}
	if err := fsutil.AtomicWrite(filepath.Join(rescue, ".farrow", "project.json"), markerData, 0o600); err != nil {
		_ = os.RemoveAll(rescue)
		return "", err
	}
	return rescue, nil
}

// removeProjectHusk deletes a project registry directory after verifying it
// holds only the expected post-destroy residue. Anything unexpected is
// preserved and reported.
func removeProjectHusk(root string) error {
	allowedFiles := map[string]struct{}{"project.json": {}, "project.lock": {}, "events.jsonl": {}}
	allowedDirs := map[string]struct{}{"nodes": {}, "keys": {}, "persistent-disks": {}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if _, ok := allowedDirs[name]; !ok {
				return fmt.Errorf("%w: %s", errProjectResidue, filepath.Join(root, name))
			}
			children, childErr := os.ReadDir(filepath.Join(root, name))
			if childErr != nil {
				return childErr
			}
			if len(children) != 0 {
				return fmt.Errorf("%w: %s is not empty", errProjectResidue, filepath.Join(root, name))
			}
			continue
		}
		if _, ok := allowedFiles[name]; !ok {
			return fmt.Errorf("%w: %s", errProjectResidue, filepath.Join(root, name))
		}
	}
	return os.RemoveAll(root)
}

// removeWorkspaceMarker deletes the .farrow marker of a workspace directory
// when it still identifies the removed project, then prunes the directory if
// that emptied it. A mismatched or foreign marker is left untouched. The
// registry side may already be gone, so the workspace marker is read
// directly rather than through project.Open.
func removeWorkspaceMarker(marker project.Marker) error {
	if marker.WorkDir == "" {
		return nil
	}
	markerDir := filepath.Join(marker.WorkDir, ".farrow")
	data, err := os.ReadFile(filepath.Join(markerDir, "project.json"))
	if err != nil {
		return nil
	}
	var decoded project.Marker
	if json.Unmarshal(data, &decoded) != nil || !decoded.SameIdentity(marker) {
		return nil
	}
	return removeMarkerDirectory(markerDir)
}

func removeMarkerDirectory(markerDir string) error {
	for _, name := range []string{"project.json", ".gitignore"} {
		if err := os.Remove(filepath.Join(markerDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// A non-empty .farrow holds user files; keep it in that case.
	if err := os.Remove(markerDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return nil
}

// destroyProjectFromWorkspace runs the ordinary destructive lifecycle for the
// project anchored at workDir: destroy (stopping first), delete persistent
// disks, purge keys.
func destroyProjectFromWorkspace(ctx context.Context, workDir string, stderr io.Writer) error {
	projectValue, err := project.Open(workDir)
	if err != nil {
		return err
	}
	store := state.Store{Project: projectValue}
	projectState, stateErr := store.ReadProject()
	switch {
	case stateErr == nil && projectState.Resolved.Network == "private":
		operationID, idErr := project.NewUUID()
		if idErr != nil {
			return idErr
		}
		manager := privatevm.Manager{CWD: workDir, FarrowVersion: version.Version, OperationID: operationID}
		if _, err := manager.Destroy(ctx); err != nil {
			return err
		}
		if _, err := manager.DeletePersistent(ctx); err != nil {
			return err
		}
	case stateErr == nil:
		operationID, idErr := project.NewUUID()
		if idErr != nil {
			return idErr
		}
		manager := quick.Manager{CWD: workDir, FarrowVersion: version.Version, OperationID: operationID}
		if _, err := manager.Destroy(ctx); err != nil {
			return err
		}
		if _, err := manager.DeletePersistent(ctx); err != nil {
			return err
		}
	case missingPathError(stateErr):
		debugf(stderr, "project %s has no resolved state; removing registration only", projectValue.Marker.ProjectID)
	default:
		return stateErr
	}
	if _, err := (privatevm.Manager{CWD: workDir, FarrowVersion: version.Version}).PurgeKeys(ctx, true); err != nil {
		var keyState *privatevm.KeyPurgeStateError
		if !errors.As(err, &keyState) {
			return err
		}
		return fmt.Errorf("project keys cannot be purged yet: %w", err)
	}
	return nil
}

func missingPathError(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var pathError *os.PathError
	return errors.As(err, &pathError) && errors.Is(pathError.Err, os.ErrNotExist)
}

// removeRegisteredProject is the complete data-root-side removal: destroy
// through a rescue workspace, verify the residue, delete the registration,
// and clean a still-matching workspace marker.
func removeRegisteredProject(ctx context.Context, registered project.Project, stderr io.Writer) error {
	rescue, err := materializeRescueWorkspace(registered)
	if err != nil {
		return err
	}
	defer os.RemoveAll(rescue)
	if err := destroyProjectFromWorkspace(ctx, rescue, stderr); err != nil {
		return err
	}
	if err := removeProjectHusk(registered.Root); err != nil {
		return err
	}
	return removeWorkspaceMarker(registered.Marker)
}

func adminDataRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if current, openErr := project.Open(cwd); openErr == nil {
		return current.DataRoot, nil
	}
	return project.ResolveDataRoot(cwd, nil)
}

func runProjectRemove(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("project rm", stderr)
	force := flags.Bool("force", false, "confirm complete project removal, including persistent disks and keys")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: farrow project rm <project-id> --force")
		return exitUsage
	}
	projectID := flags.Arg(0)
	if !project.ValidUUID(projectID) {
		fmt.Fprintln(stderr, "project rm requires the registered project UUID; run `farrow list --json`")
		return exitUsage
	}
	dataRoot, err := adminDataRoot()
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	registered, err := findRegisteredProject(dataRoot, projectID)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitConflict
	}
	fmt.Fprintf(stderr, "project %s (%s)\nregistry: %s\nworkspace: %s\nremoval deletes VM artifacts, persistent disks, keys, and the registration\n", projectID, registered.Marker.Name, registered.Root, registered.Marker.WorkDir)
	if err := confirmCLIAction(*force, "project rm", stderr); err != nil {
		errorf(stderr, "%v", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	progressItem := startProgress(ctx, stderr, "Destroying and deregistering the project")
	err = removeRegisteredProject(ctx, registered, stderr)
	progressItem.Stop(err)
	if err != nil {
		errorf(stderr, "%v", err)
		if errors.Is(err, errProjectResidue) {
			return exitIntegrity
		}
		return exitRuntime
	}
	if structuredOutput(stdout, *jsonOutput) {
		return encodeJSON(stdout, stderr, struct {
			ProjectID string `json:"project_id"`
			Removed   bool   `json:"removed"`
		}{projectID, true})
	}
	fmt.Fprintf(stdout, "removed project %s\n", projectID)
	return exitOK
}

func runProjectPrune(args []string, stdout, stderr io.Writer) int {
	flags := newCommandFlagSet("project prune", stderr)
	dryRun := flags.Bool("dry-run", false, "list orphaned projects without removing them (the default)")
	apply := flags.Bool("yes", false, "remove the listed orphaned projects")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || (*dryRun && *apply) {
		fmt.Fprintln(stderr, "usage: farrow project prune [--dry-run|--yes]")
		return exitUsage
	}
	dataRoot, err := adminDataRoot()
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	discovery, err := project.Discover(dataRoot)
	if err != nil {
		errorf(stderr, "%v", err)
		return exitRuntime
	}
	cwd, _ := os.Getwd()
	currentID := ""
	if current, openErr := project.Open(cwd); openErr == nil {
		currentID = current.Marker.ProjectID
	}
	type pruneRow struct {
		ProjectID string `json:"project_id"`
		Name      string `json:"name,omitempty"`
		WorkDir   string `json:"work_dir,omitempty"`
		Orphan    string `json:"orphan"`
		Removed   bool   `json:"removed"`
		Error     string `json:"error,omitempty"`
	}
	rows := make([]pruneRow, 0)
	for _, candidate := range discovery.Projects {
		orphan := quick.OrphanState(candidate.Marker, candidate.Marker.ProjectID == currentID)
		if orphan == "" {
			continue
		}
		rows = append(rows, pruneRow{ProjectID: candidate.Marker.ProjectID, Name: candidate.Marker.Name, WorkDir: candidate.Marker.WorkDir, Orphan: orphan})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	exit := exitOK
	for index := range rows {
		row := &rows[index]
		// A schema-1 marker cannot prove its workspace is gone; it is listed but
		// never removed automatically. Upgrade it in place or use project rm.
		removable := row.Orphan == "workdir-missing" || row.Orphan == "workdir-mismatch"
		if !*apply || !removable {
			continue
		}
		registered, findErr := findRegisteredProject(dataRoot, row.ProjectID)
		if findErr != nil {
			row.Error = findErr.Error()
			exit = exitPartial
			continue
		}
		if err := removeRegisteredProject(ctx, registered, stderr); err != nil {
			row.Error = err.Error()
			exit = exitPartial
			continue
		}
		row.Removed = true
	}
	if structuredOutput(stdout, *jsonOutput) {
		if code := encodeJSON(stdout, stderr, struct {
			DataRoot string     `json:"data_root"`
			Apply    bool       `json:"apply"`
			Projects []pruneRow `json:"projects"`
		}{dataRoot, *apply, rows}); code != exitOK {
			return code
		}
		return exit
	}
	textField(stdout, 12, "data root", dataRoot)
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "no orphaned projects")
		return exitOK
	}
	for _, row := range rows {
		verb := "would remove"
		switch {
		case row.Removed:
			verb = "removed"
		case row.Error != "":
			verb = "failed"
		case row.Orphan == "unknown-workdir":
			verb = "listed only"
		}
		fmt.Fprintf(stdout, "%s %s (%s) orphan=%s workdir=%s\n", verb, row.ProjectID, row.Name, row.Orphan, row.WorkDir)
		if row.Error != "" {
			fmt.Fprintf(stdout, "  error: %s\n", row.Error)
		}
		if row.Orphan == "unknown-workdir" {
			fmt.Fprintln(stdout, "  schema-1 marker records no workspace; run `farrow project upgrade-state --yes` in its directory, or `farrow project rm <id> --force`")
		}
	}
	if !*apply {
		fmt.Fprintln(stdout, "rerun with --yes to remove the removable orphans above")
	}
	return exit
}

// purgeCurrentProject removes what `destroy --force` deliberately preserves:
// keys, the registration directory, and the workspace marker. Used by
// `destroy --force --purge` after a successful destroy.
func purgeCurrentProject(ctx context.Context, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	projectValue, err := project.Open(cwd)
	if err != nil {
		return err
	}
	if _, err := (privatevm.Manager{FarrowVersion: version.Version}).PurgeKeys(ctx, true); err != nil {
		return err
	}
	if err := removeProjectHusk(projectValue.Root); err != nil {
		return err
	}
	if err := removeMarkerDirectory(projectValue.MarkerDir); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "purged project keys, registration, and workspace marker")
	return nil
}

package private

import (
	"context"
	"os"
	"path/filepath"

	"github.com/pgsty/farrow/internal/lock"
	"github.com/pgsty/farrow/internal/state"
)

// Deployment locates the single global deployment. Its root IS the data root:
// state, nodes, keys, and retained disks all live directly under it.
type Deployment struct {
	Root string
}

func (d Deployment) NodeDir(name string) (string, error) {
	return state.Store{Root: d.Root}.NodeDir(name)
}

func (d Deployment) EnsureNodeDir(name string) (string, error) {
	return state.Store{Root: d.Root}.EnsureNodeDir(name)
}

// openDeployment resolves the deployment root; with create it also ensures
// the root and lock directories exist.
func openDeployment(create bool) (Deployment, error) {
	root, err := state.ResolveDataRoot()
	if err != nil {
		return Deployment{}, err
	}
	value := Deployment{Root: root}
	if create {
		if err := (state.Store{Root: root}).EnsureRoot(); err != nil {
			return Deployment{}, err
		}
	}
	return value, nil
}

func deploymentLockPath(root string) string { return filepath.Join(root, "locks", "lock") }

// acquireDeploymentLock serializes every mutating farrow invocation on the
// one global deployment. flock releases automatically on process exit.
func acquireDeploymentLock(ctx context.Context, root string, shared bool) (*lock.File, error) {
	if err := os.MkdirAll(filepath.Join(root, "locks"), 0o700); err != nil {
		return nil, err
	}
	return lock.Acquire(ctx, deploymentLockPath(root), shared)
}

// Open returns the deployment handle without creating or locking anything.
func Open() (Deployment, error) { return openDeployment(false) }

// AcquireLock takes the deployment lock; the caller must Release it.
func AcquireLock(ctx context.Context, deployment Deployment, shared bool) (*lock.File, error) {
	return acquireDeploymentLock(ctx, deployment.Root, shared)
}

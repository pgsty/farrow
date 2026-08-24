package private

import (
	"context"

	"github.com/pgsty/piglet/internal/quick"
)

type KeyPurgeAction = quick.KeyPurgeAction
type KeyPurgeReport = quick.KeyPurgeReport
type KeyPurgeStateError = quick.KeyPurgeStateError
type KeyPurgeIntegrityError = quick.KeyPurgeIntegrityError

// PurgeKeys remains available after private Destroy removes resolved.json: the
// matched workspace/data-root project markers are the authority, and the
// shared quick/private boundary independently proves complete node absence.
func (m Manager) PurgeKeys(ctx context.Context, apply bool) (KeyPurgeReport, error) {
	projectValue, err := m.openProject(false)
	if err != nil {
		return KeyPurgeReport{}, err
	}
	return quick.PurgeProjectKeys(ctx, projectValue, apply)
}

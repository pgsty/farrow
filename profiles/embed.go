// Package profiles exposes the immutable profile assets compiled into Farrow.
package profiles

import "embed"

// FS contains the profile catalog and every supported profile. Callers should
// parse profile YAML through internal/config so strict-field validation remains
// the single source of truth.
//
//go:embed catalog.json *.yaml
var FS embed.FS

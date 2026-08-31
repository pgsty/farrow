// Package naming owns identifiers shared across Farrow's filesystem, state,
// QEMU, provisioning, and guest-rendering boundaries.
package naming

import "regexp"

var nodeNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidNodeName reports whether name is a safe DNS label and path component.
func ValidNodeName(name string) bool {
	return nodeNamePattern.MatchString(name)
}

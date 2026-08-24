// Package openssh owns OpenSSH's configuration-value quoting rules.
package openssh

import (
	"errors"
	"strings"
)

// QuoteConfigValue quotes one value for OpenSSH's internal option/config
// parser. This is required even when the outer process argv is already split:
// options such as UserKnownHostsFile parse whitespace-separated path lists.
func QuoteConfigValue(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("openssh config value must be a non-empty single line")
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`, nil
}

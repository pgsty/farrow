package quick

import (
	"fmt"
	"regexp"
)

const defaultSSHUser = "dba"

var sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// resolvedSSHUser preserves the historical Quick login for state documents
// written before ssh_user was persisted, while rejecting unsafe explicit
// values before they can reach OpenSSH argv or generated configuration.
func resolvedSSHUser(user string) (string, error) {
	if user == "" {
		return defaultSSHUser, nil
	}
	if !sshUserPattern.MatchString(user) {
		return "", fmt.Errorf("invalid quick SSH user %q", user)
	}
	return user, nil
}

func statusSSHUser(user string) string {
	resolved, err := resolvedSSHUser(user)
	if err != nil {
		return ""
	}
	return resolved
}

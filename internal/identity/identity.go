// Package identity derives stable non-secret device identities per project.
package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
)

func digest(projectID, purpose, node, item string) ([]byte, error) {
	if projectID == "" || node == "" || item == "" {
		return nil, errors.New("project, node, and item must be non-empty")
	}
	h := hmac.New(sha256.New, []byte(projectID))
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s", purpose, node, item)
	return h.Sum(nil), nil
}

// DiskSerial returns the PRD-required first 96 HMAC bits as 20 lowercase
// RFC4648 base32 characters without padding.
func DiskSerial(projectID, node, disk string) (string, error) {
	sum, err := digest(projectID, "disk", node, disk)
	if err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:12])), nil
}

// MAC returns a deterministic locally administered unicast address.
func MAC(projectID, node, nic string) (string, error) {
	sum, err := digest(projectID, "mac", node, nic)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", sum[0], sum[1], sum[2], sum[3], sum[4]), nil
}

// Package identity derives stable, non-secret identifiers for the single
// deployment: NIC MAC addresses, disk serials, and v4 UUIDs.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// NIC roles accepted by MAC. The role byte makes the two adapters of one node
// distinguishable at a glance: 0x4d ('M') management, 0x50 ('P') private.
const (
	NICManagement = "management"
	NICPrivate    = "private"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// MAC returns the deterministic locally administered unicast address for one
// node NIC, encoding the node's fixed private IPv4 directly: reading a MAC in
// an ARP table or a QEMU argv identifies the node without any lookup.
func MAC(address, nic string) (string, error) {
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("MAC derivation requires an IPv4 address, got %q", address)
	}
	role := byte(0)
	switch nic {
	case NICManagement:
		role = 0x4d
	case NICPrivate:
		role = 0x50
	default:
		return "", fmt.Errorf("unknown NIC role %q", nic)
	}
	v4 := ip.To4()
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", role, v4[0], v4[1], v4[2], v4[3]), nil
}

// DiskSerial returns a stable 20-character lowercase base32 serial derived
// from the node and disk names, stable across recreates.
func DiskSerial(node, disk string) (string, error) {
	if node == "" || disk == "" {
		return "", errors.New("node and disk must be non-empty")
	}
	sum := sha256.Sum256([]byte(node + "\x00" + disk))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:12])), nil
}

// NewUUID returns a random version-4 UUID.
func NewUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

// ValidUUID reports whether value is a canonical version-4 UUID.
func ValidUUID(value string) bool { return uuidPattern.MatchString(value) }

// DeploymentID is the fixed identity of the single global deployment,
// carried transitionally by documents that still require a UUID field.
const DeploymentID = "00000000-0000-4000-8000-000000000000"

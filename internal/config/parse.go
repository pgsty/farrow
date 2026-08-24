// Package config contains strict user-facing scalar parsers shared by CLI and
// future YAML decoding.
package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pgsty/piglet/internal/spec"
)

var sizeUnits = []struct {
	name       string
	multiplier int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000}, {"TB", 1000 * 1000 * 1000 * 1000},
	{"B", 1},
}

func ParseSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	for _, unit := range sizeUnits {
		if !strings.HasSuffix(value, unit.name) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(value, unit.name))
		if number == "" || strings.ContainsAny(number, ".+-") {
			return 0, fmt.Errorf("size %q must be a positive integer plus unit", value)
		}
		parsed, err := strconv.ParseInt(number, 10, 64)
		if err != nil || parsed <= 0 || parsed > (1<<63-1)/unit.multiplier {
			return 0, fmt.Errorf("invalid or overflowing size %q", value)
		}
		return parsed * unit.multiplier, nil
	}
	return 0, fmt.Errorf("size %q requires B, KiB, MiB, GiB, TiB, KB, MB, GB, or TB", value)
}

// ParseForward accepts host:guest or bind:host:guest. IPv6 binds use
// [address]:host:guest. Protocol is TCP in v1.
func ParseForward(value string) (spec.Forward, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return spec.Forward{}, errors.New("forward is empty")
	}
	bind := "127.0.0.1"
	var hostText, guestText string
	if strings.HasPrefix(value, "[") {
		closing := strings.Index(value, "]:")
		if closing < 0 {
			return spec.Forward{}, fmt.Errorf("invalid bracketed forward %q", value)
		}
		bind = value[1:closing]
		ports := strings.Split(value[closing+2:], ":")
		if len(ports) != 2 {
			return spec.Forward{}, fmt.Errorf("invalid forward %q", value)
		}
		hostText, guestText = ports[0], ports[1]
	} else {
		parts := strings.Split(value, ":")
		switch len(parts) {
		case 2:
			hostText, guestText = parts[0], parts[1]
		case 3:
			bind, hostText, guestText = parts[0], parts[1], parts[2]
		default:
			return spec.Forward{}, fmt.Errorf("invalid forward %q", value)
		}
	}
	if net.ParseIP(bind) == nil {
		return spec.Forward{}, fmt.Errorf("invalid forward bind address %q", bind)
	}
	host, err := strconv.ParseUint(hostText, 10, 16)
	if err != nil || host == 0 {
		return spec.Forward{}, fmt.Errorf("invalid host port %q", hostText)
	}
	guest, err := strconv.ParseUint(guestText, 10, 16)
	if err != nil || guest == 0 {
		return spec.Forward{}, fmt.Errorf("invalid guest port %q", guestText)
	}
	return spec.Forward{Bind: bind, Host: uint16(host), Guest: uint16(guest), Protocol: "tcp"}, nil
}

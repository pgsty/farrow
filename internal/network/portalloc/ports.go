// Package portalloc implements deterministic host-port allocation.
package portalloc

import "fmt"

const candidateSteps = 5

// Candidates implements preferred+n*10000 for n=0..4 and rejects overflow.
func Candidates(preferred uint16) []uint16 {
	ports := make([]uint16, 0, candidateSteps)
	for n := 0; n < candidateSteps; n++ {
		candidate := uint32(preferred) + uint32(n*10000)
		if candidate > 65535 || candidate == 0 {
			break
		}
		ports = append(ports, uint16(candidate))
	}
	return ports
}

// Choose returns the first candidate accepted by available. The caller owns
// the allocator lock and persistence; this function is deterministic and pure
// apart from the supplied availability probe.
func Choose(preferred uint16, available func(uint16) bool) (uint16, error) {
	if available == nil {
		return 0, fmt.Errorf("port availability probe is nil")
	}
	for _, port := range Candidates(preferred) {
		if available(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no loopback port available for preferred %d within the finite candidate set", preferred)
}

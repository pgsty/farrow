package qemu

import (
	"errors"
	"regexp"
)

var shareDevicePattern = regexp.MustCompile(`(^|[^A-Za-z0-9_.-])virtio-9p-pci([^A-Za-z0-9_.-]|$)`)

// ValidateShareDeviceHelp proves that the exact QEMU binary exposes the 9p
// PCI transport. Version numbers alone are insufficient because distributors
// may compile VirtFS out.
func ValidateShareDeviceHelp(output string) error {
	if shareDevicePattern.MatchString(output) {
		return nil
	}
	return errors.New("QEMU binary lacks virtio-9p-pci (VirtFS may be disabled)")
}

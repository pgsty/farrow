package qemu

import "testing"

func TestValidateShareDeviceHelp(t *testing.T) {
	t.Parallel()
	if err := ValidateShareDeviceHelp(`name "virtio-9p-pci", bus PCI, desc "Virtio 9p Transport"`); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		`name "virtio-net-pci", bus PCI`,
		`name "virtio-9p-pci-transitional", bus PCI`,
		`name "prefix-virtio-9p-pci", bus PCI`,
	} {
		if err := ValidateShareDeviceHelp(output); err == nil {
			t.Fatalf("QEMU help without the exact VirtFS device was accepted: %q", output)
		}
	}
}

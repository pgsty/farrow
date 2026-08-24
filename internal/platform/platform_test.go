package platform

import "testing"

func TestResolveSupportMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		os, arch, binary, accel string
		tier                    Tier
	}{
		{"darwin", "arm64", "qemu-system-aarch64", "hvf", Tier1},
		{"darwin", "amd64", "qemu-system-x86_64", "hvf", Tier2},
		{"linux", "amd64", "qemu-system-x86_64", "kvm", Tier1},
		{"linux", "arm64", "qemu-system-aarch64", "kvm", Tier2},
	}
	for _, tt := range tests {
		profile, err := Resolve(tt.os, tt.arch)
		if err != nil {
			t.Fatalf("Resolve(%s/%s): %v", tt.os, tt.arch, err)
		}
		if profile.QEMUBinary != tt.binary || profile.Accelerator != tt.accel || profile.Tier != tt.tier {
			t.Errorf("Resolve(%s/%s) = %#v", tt.os, tt.arch, profile)
		}
	}
	if _, err := Resolve("windows", "amd64"); err == nil {
		t.Fatal("windows host unexpectedly supported")
	}
}

func TestParseQEMUVersion(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]Version{
		"QEMU emulator version 10.1.0":               {10, 1, 0},
		"QEMU emulator version 8.2.1 (Homebrew)":     {8, 2, 1},
		"qemu-img version 6.2.0 (Debian 1:6.2+dfsg)": {6, 2, 0},
	} {
		got, err := ParseQEMUVersion(input)
		if err != nil {
			t.Fatalf("ParseQEMUVersion(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("ParseQEMUVersion(%q) = %s, want %s", input, got, want)
		}
	}
	if _, err := ParseQEMUVersion("not qemu"); err == nil {
		t.Fatal("invalid banner unexpectedly parsed")
	}
}

func TestVersionAtLeast(t *testing.T) {
	t.Parallel()
	if !(Version{8, 2, 1}).AtLeast(Version{8, 2, 1}) {
		t.Fatal("equal version should satisfy floor")
	}
	if (Version{8, 2, 0}).AtLeast(Version{8, 2, 1}) {
		t.Fatal("older patch should not satisfy floor")
	}
}

func TestFirmwareCandidatesCoverNativeUEFIArchitectures(t *testing.T) {
	t.Parallel()
	amd64, _ := Resolve("linux", "amd64")
	arm64, _ := Resolve("darwin", "arm64")
	if len(FirmwareCandidates(amd64)) == 0 || len(FirmwareCandidates(arm64)) == 0 {
		t.Fatal("UEFI candidate matrix is incomplete")
	}
	if firmware, err := FindFirmwareForBoot(amd64, "bios"); err != nil || firmware != (Firmware{}) {
		t.Fatalf("amd64 BIOS selection = %#v, %v", firmware, err)
	}
	if _, err := FindFirmwareForBoot(arm64, "bios"); err == nil {
		t.Fatal("required-UEFI arm64 profile accepted BIOS")
	}
}

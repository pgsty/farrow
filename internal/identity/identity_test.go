package identity

import (
	"regexp"
	"testing"
)

func TestDiskSerialContract(t *testing.T) {
	t.Parallel()
	serial, err := DiskSerial("018f4b8e-1234-7abc-9def-0123456789ab", "meta", "data")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z2-7]{20}$`).MatchString(serial) {
		t.Fatalf("serial %q does not match 20-char lowercase base32", serial)
	}
	again, _ := DiskSerial("018f4b8e-1234-7abc-9def-0123456789ab", "meta", "data")
	other, _ := DiskSerial("018f4b8e-1234-7abc-9def-0123456789ab", "meta", "data2")
	if serial != again || serial == other {
		t.Fatalf("serial stability/uniqueness failure: %q %q %q", serial, again, other)
	}
}

func TestMACContract(t *testing.T) {
	t.Parallel()
	mac, err := MAC("project", "meta", "management")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^02(:[0-9a-f]{2}){5}$`).MatchString(mac) {
		t.Fatalf("invalid deterministic MAC %q", mac)
	}
}

func TestIdentityRejectsEmptyParts(t *testing.T) {
	t.Parallel()
	if _, err := DiskSerial("", "meta", "data"); err == nil {
		t.Fatal("empty project unexpectedly accepted")
	}
}

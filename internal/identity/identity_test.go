package identity

import (
	"regexp"
	"testing"
)

func TestDiskSerialContract(t *testing.T) {
	t.Parallel()
	serial, err := DiskSerial("meta", "data")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z2-7]{20}$`).MatchString(serial) {
		t.Fatalf("serial %q does not match 20-char lowercase base32", serial)
	}
	again, _ := DiskSerial("meta", "data")
	other, _ := DiskSerial("meta", "data2")
	if serial != again || serial == other {
		t.Fatalf("serial stability/uniqueness failure: %q %q %q", serial, again, other)
	}
}

func TestMACContract(t *testing.T) {
	t.Parallel()
	mac, err := MAC("10.10.10.10", NICManagement)
	if err != nil {
		t.Fatal(err)
	}
	if mac != "02:4d:0a:0a:0a:0a" {
		t.Fatalf("unexpected management MAC %q", mac)
	}
	private, err := MAC("10.10.10.10", NICPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if private != "02:50:0a:0a:0a:0a" {
		t.Fatalf("unexpected private MAC %q", private)
	}
	if _, err := MAC("not-an-ip", NICManagement); err == nil {
		t.Fatal("non-IP address unexpectedly accepted")
	}
	if _, err := MAC("10.10.10.10", "bogus"); err == nil {
		t.Fatal("unknown NIC role unexpectedly accepted")
	}
}

func TestIdentityRejectsEmptyParts(t *testing.T) {
	t.Parallel()
	if _, err := DiskSerial("", "data"); err == nil {
		t.Fatal("empty node unexpectedly accepted")
	}
	if _, err := DiskSerial("meta", ""); err == nil {
		t.Fatal("empty disk unexpectedly accepted")
	}
}

func TestUUIDHelpers(t *testing.T) {
	t.Parallel()
	value, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidUUID(value) {
		t.Fatalf("NewUUID produced invalid UUID %q", value)
	}
	if ValidUUID("not-a-uuid") {
		t.Fatal("invalid UUID unexpectedly accepted")
	}
}

package qemu

import (
	"strings"
	"testing"

	"github.com/pgsty/piglet/internal/platform"
)

func testConfig(t *testing.T, goos, arch string) Config {
	t.Helper()
	profile, err := platform.Resolve(goos, arch)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Profile: profile, Binary: "/opt/qemu/bin/" + profile.QEMUBinary,
		Name: "meta", UUID: "018f4b8e-1234-7abc-9def-0123456789ab",
		CPUs: 2, Memory: 4 * 1024 * MiB,
		Root: Disk{Path: "/data/root.qcow2", Serial: "root0000000000000001"},
		Data: []Disk{{Path: "/data/data.qcow2", Serial: "abcde234567abcde2345"}},
		Seed: "/data/seed.iso", QMP: "/tmp/piglet/p/m/qmp.sock",
		PIDFile: "/tmp/piglet/p/m/qemu.pid", SerialLog: "/data/serial.log",
		MgmtMAC:  "02:11:22:33:44:55",
		Forwards: []Forward{{Bind: "127.0.0.1", Host: 2222, Guest: 22}, {Bind: "127.0.0.1", Host: 15432, Guest: 5432}},
		Detach:   true,
	}
	if profile.RequiresUEFI {
		config.Firmware = &Firmware{Code: "/opt/qemu/share/edk2-aarch64-code.fd", Vars: "/data/nvram.fd"}
	}
	return config
}

func TestBuildDarwinArm64UserInvocation(t *testing.T) {
	t.Parallel()
	invocation, err := Build(testConfig(t, "darwin", "arm64"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(invocation.Args, "\n")
	for _, want := range []string{
		"qemu-system-aarch64", "virt", "hvf", "-daemonize",
		`"driver":"qcow2"`, `"filename":"/data/root.qcow2"`,
		"virtio-blk-pci,drive=data0,serial=abcde234567abcde2345",
		"scsi-cd,drive=seed,bus=seed-scsi.0",
		"hostfwd=tcp:127.0.0.1:2222-:22",
		"hostfwd=tcp:127.0.0.1:15432-:5432",
	} {
		if !strings.Contains(invocation.Binary+"\n"+joined, want) {
			t.Errorf("invocation missing %q:\n%s\n%s", want, invocation.Binary, joined)
		}
	}
}

func TestBuildLinuxAMD64DoesNotAddFirmware(t *testing.T) {
	t.Parallel()
	invocation, err := Build(testConfig(t, "linux", "amd64"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(invocation.Args, "\n")
	if !strings.Contains(joined, "kvm") || strings.Contains(joined, "pflash") {
		t.Fatalf("unexpected linux invocation:\n%s", joined)
	}
}

func TestBuildPrivateStreamAndFDBackends(t *testing.T) {
	t.Parallel()
	stream := testConfig(t, "darwin", "arm64")
	stream.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ee", StreamSocket: "/private/var/run/piglet-vmnet.sock", ReconnectMS: 1000}
	invocation, err := Build(stream)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(invocation.Args, "\n")
	if !strings.Contains(joined, "stream,id=private,server=off,addr.type=unix,addr.path=/private/var/run/piglet-vmnet.sock,reconnect-ms=1000") || !strings.Contains(joined, "virtio-net-pci,netdev=private,mac=02:aa:bb:cc:dd:ee") {
		t.Fatalf("private stream missing:\n%s", joined)
	}

	fd := testConfig(t, "darwin", "arm64")
	fd.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ef", FD: 3}
	invocation, err = Build(fd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(invocation.Args, "\n"), "socket,id=private,fd=3") || !invocation.UsesPrivateFD3() {
		t.Fatal("private FD fallback missing")
	}
}

func TestBuildLinuxPrivateBridgeBackend(t *testing.T) {
	t.Parallel()
	config := testConfig(t, "linux", "amd64")
	config.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:f0", Bridge: "piglet0", BridgeHelper: "/usr/libexec/qemu-bridge-helper"}
	invocation, err := Build(config)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(invocation.Args, "\n")
	if !strings.Contains(joined, "bridge,id=private,br=piglet0,helper=/usr/libexec/qemu-bridge-helper") {
		t.Fatalf("Linux private bridge missing:\n%s", joined)
	}
}

func TestBuildRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	t.Parallel()
	tests := []func(*Config){
		func(c *Config) { c.Name = "../meta" },
		func(c *Config) { c.UUID = "not-a-uuid" },
		func(c *Config) { c.Memory = 511 * MiB },
		func(c *Config) { c.QMP = "/" + strings.Repeat("q", 110) },
		func(c *Config) { c.Data = append(c.Data, c.Data[0]) },
		func(c *Config) { c.Forwards = append(c.Forwards, c.Forwards[0]) },
		func(c *Config) { c.Firmware = nil },
		func(c *Config) { c.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ee", FD: 4} },
		func(c *Config) {
			c.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ee", StreamSocket: "/tmp/a", ReconnectMS: 0}
		},
		func(c *Config) { c.Private = &PrivateNetwork{MAC: "bad", FD: 3} },
		func(c *Config) {
			c.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ee", Bridge: "br0", BridgeHelper: "/usr/libexec/qemu-bridge-helper"}
		},
	}
	for i, mutate := range tests {
		config := testConfig(t, "darwin", "arm64")
		mutate(&config)
		if _, err := Build(config); err == nil {
			t.Errorf("unsafe config %d unexpectedly accepted", i)
		}
	}
}

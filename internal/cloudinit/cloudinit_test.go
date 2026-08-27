package cloudinit

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEbV2dummydummydummydummydummydummydummy farrow-test"

func testInput() Input {
	return Input{
		Node: "meta", Hostname: "meta", Generation: 1,
		SpecHash: strings.Repeat("a", 64), SSHUser: "dba", PublicKey: testPublicKey,
		MgmtMAC: "02:11:22:33:44:55",
		Disks:   []Disk{{Serial: "abcde234567abcde2345", Mount: "/data", Filesystem: "auto"}},
	}
}

func testKeyPair(t *testing.T) (string, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))) + " farrow-test", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

func TestRenderQuickContract(t *testing.T) {
	t.Parallel()
	files, err := Render(testInput())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files.entries() {
		if len(content) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	userData := string(files.UserData)
	for _, want := range []string{"#cloud-config", `name: "dba"`, "uid: 88", "primary_group: admin", "create_groups: false", "/usr/sbin/groupadd --gid 88 admin", "/usr/sbin/groupmod --gid 88 admin", "lock_passwd: true", "ssh_deletekeys: false", "ntp:\n  enabled: true", "/dev/disk/by-id/virtio-", "UUID=%s", "defaults,nofail", `mountpoint -q "${mountpoint}"`, "xfs_growfs", "resize2fs", "/usr/local/libexec/farrow-network-check", "/usr/local/libexec/farrow-identity-contract", "login identity", "/dev/tcp/example.com/80", "/var/lib/farrow/ready.json"} {
		if !strings.Contains(userData, want) {
			t.Errorf("user-data missing %q", want)
		}
	}
	if strings.Contains(userData, "BEGIN OPENSSH PRIVATE KEY") || strings.Contains(userData, "StrictHostKeyChecking no") {
		t.Fatal("quick seed contains a private key or disables host-key checking")
	}
	if strings.Contains(userData, "farrow-init-shares") || strings.Contains(userData, "FARROW SHARES") || strings.Contains(userData, "/var/lib/farrow/error.json") {
		t.Fatal("no-share quick seed contains share-specific files, markers, or traps")
	}
	if strings.Contains(string(files.NetworkConfig), "private:") {
		t.Fatal("quick network unexpectedly contains a private NIC")
	}
}

func TestRenderPrivateControlKeyBoundary(t *testing.T) {
	t.Parallel()
	input := testInput()
	input.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ee", Address: "10.10.10.10", Prefix: 24, HostAddress: "10.10.10.1"}
	input.Hosts = []Host{{Name: "meta", Address: "10.10.10.10"}, {Name: "node-1", Address: "10.10.10.11"}}
	input.Control = true
	input.PublicKey, input.PrivateKey = testKeyPair(t)
	files, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files.NetworkConfig), `"10.10.10.10/24"`) || !strings.Contains(string(files.NetworkConfig), "accept-ra: false") || !strings.Contains(string(files.NetworkConfig), "link-local: []") || strings.Contains(string(files.NetworkConfig), "gateway") || strings.Contains(string(files.NetworkConfig), "nameservers") {
		t.Fatalf("private network contract mismatch:\n%s", files.NetworkConfig)
	}
	if !bytes.Contains(files.UserData, []byte("farrow-private-contract")) || !bytes.Contains(files.UserData, []byte("private0 owns a default route")) {
		t.Fatal("private runtime contract checker missing")
	}
	if bytes.Contains(files.UserData, []byte(`ping -c`)) || !bytes.Contains(files.UserData, []byte(`printf 'farrow-arp-refresh\n' >"/dev/udp/${expected_host}/9"`)) || !bytes.Contains(files.UserData, []byte(`expected_host="10.10.10.1"`)) {
		t.Fatal("private ready contract does not refresh/verify the host neighbor path")
	}
	if !bytes.Contains(files.UserData, []byte("-----BEGIN PRIVATE KEY-----")) ||
		!bytes.Contains(files.UserData, []byte("StrictHostKeyChecking accept-new")) {
		t.Fatal("control lateral SSH contract missing")
	}
	for _, want := range []string{
		`path: "/var/lib/farrow/control-id_ed25519"`,
		`path: "/var/lib/farrow/control-ssh-config"`,
		`path: "/usr/local/libexec/farrow-install-control-ssh"`,
		`entry=$(getent passwd "${user}")`,
		`install -d -o "${uid}" -g "${gid}" -m 0700 "${ssh_dir}"`,
		`rm -f -- /var/lib/farrow/control-id_ed25519 /var/lib/farrow/control-ssh-config`,
		`path: "/usr/local/libexec/farrow-finalize"`,
		`- ['/usr/local/libexec/farrow-finalize']`,
	} {
		if !bytes.Contains(files.UserData, []byte(want)) {
			t.Errorf("control seed missing %q", want)
		}
	}
	if bytes.Contains(files.UserData, []byte(`owner: "dba:dba"`)) ||
		bytes.Contains(files.UserData, []byte(`path: "/home/dba/.ssh/id_ed25519"`)) {
		t.Fatal("write_files refers to the login user before users-groups creates it")
	}
	finalizer := renderFinalizeScript(true, true, true)
	identityIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-identity-contract")
	networkIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-network-check")
	diskIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-init-disks")
	shareIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-init-shares")
	installIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-install-control-ssh")
	privateIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-private-contract")
	readyIndex := strings.Index(finalizer, "/usr/local/libexec/farrow-ready")
	if identityIndex < 0 || networkIndex <= identityIndex || diskIndex <= networkIndex || shareIndex <= diskIndex || installIndex <= shareIndex || privateIndex <= installIndex || readyIndex <= privateIndex {
		t.Fatalf("private finalizer is not fail-closed:\n%s", finalizer)
	}

	input.Control = false
	if _, err := Render(input); err == nil {
		t.Fatal("private key injected into non-control guest")
	}
}

func TestRenderCustomUserDoesNotClaimPigstyIdentity(t *testing.T) {
	t.Parallel()
	input := testInput()
	input.SSHUser = "operator"
	files, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	userData := string(files.UserData)
	if strings.Contains(userData, "uid: 88") || strings.Contains(userData, "primary_group: admin") || strings.Contains(userData, "groupadd --gid 88 admin") {
		t.Fatalf("custom user received Pigsty identity contract:\n%s", userData)
	}
	if !strings.Contains(userData, `user="operator"`) && !strings.Contains(userData, `user='operator'`) {
		t.Fatalf("custom identity checker is missing operator:\n%s", userData)
	}
}

func TestWriteUserDataForExternalSchemaValidation(t *testing.T) {
	output := os.Getenv("FARROW_CLOUD_CONFIG_OUTPUT")
	if output == "" {
		t.Skip("set FARROW_CLOUD_CONFIG_OUTPUT to write a test-only cloud-config fixture")
	}
	files, err := Render(testInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, files.UserData, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderRejectsUnsafeMounts(t *testing.T) {
	t.Parallel()
	for _, mount := range []string{"/data/../../etc", "/data/..", "/data//nested", "/proc/data", "/var/lib/farrow/data"} {
		input := testInput()
		input.Disks[0].Mount = mount
		if _, err := Render(input); err == nil {
			t.Errorf("unsafe mount %q was accepted", mount)
		}
	}
}

func TestRenderShareContract(t *testing.T) {
	t.Parallel()
	input := testInput()
	input.Shares = []Share{
		{Tag: "farrow-0123456789abcdef0123", Guest: "/src", Readonly: false},
		{Tag: "farrow-fedcba9876543210fedc", Guest: "/reference", Readonly: true},
	}
	files, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	userData := string(files.UserData)
	for _, want := range []string{
		`path: "/usr/local/libexec/farrow-init-shares"`,
		`# BEGIN FARROW SHARES`,
		`# END FARROW SHARES`,
		`farrow-0123456789abcdef0123 /src 9p version=9p2000.L,trans=virtio,cache=none,msize=262144,access=any,nofail,nodev,nosuid,rw 0 0`,
		`farrow-fedcba9876543210fedc /reference 9p version=9p2000.L,trans=virtio,cache=none,msize=262144,access=any,nofail,nodev,nosuid,ro 0 0`,
		`runuser -u "${share_user}" -- mktemp`,
		`has_mount_option "${mounted_options}" ro`,
		`has_mount_option "${mounted_options}" rw`,
		`refuse non-empty Farrow share mountpoint`,
		`mv -f -- "${fstab_tmp}" "${fstab}"`,
		`printf '{"exit_status":%d,"line":%d}\n'`,
	} {
		if !strings.Contains(userData, want) {
			t.Errorf("share contract missing %q", want)
		}
	}
	if strings.Contains(userData, "chown -R") {
		t.Fatal("share contract recursively changes host-backed ownership")
	}
	script := renderShareScript(input.SSHUser, input.Shares)
	publishIndex := strings.Index(script, `mv -f -- "${fstab_tmp}" "${fstab}"`)
	mountIndex := strings.Index(script, `mount "${mountpoint}"`)
	if publishIndex < 0 || mountIndex <= publishIndex {
		t.Fatalf("share script mounts before atomically publishing fstab:\n%s", script)
	}
	if !strings.Contains(script, "if mountpoint -q \"${mountpoint}\"; then\n    verify_mount") {
		t.Fatalf("share script does not idempotently verify an existing mount:\n%s", script)
	}
	for _, want := range []string{
		`if [[ "${mounted_type}" == 9p ]] && is_desired_share "${mounted_source}" "${old_guest}"; then`,
		`if [[ "${mounted_source}" != "${old_tag}" || "${mounted_type}" != 9p ]] || is_desired_share "${old_tag}" "${old_guest}"; then`,
		`umount "${old_guest}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("share script does not safely distinguish expected and removed marker mounts; missing %q:\n%s", want, script)
		}
	}
	emptyScript := renderShareScript(input.SSHUser, nil)
	if emptyScript != "" {
		t.Fatalf("empty share script changed the no-share path:\n%s", emptyScript)
	}
}

func TestRenderRejectsUnsafeShareTagsAndMountOverlap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		shares []Share
	}{
		{name: "user selected tag", shares: []Share{{Tag: "source", Guest: "/src"}}},
		{name: "reserved ancestor", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/var"}}},
		{name: "ssh ancestor", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/home/dba"}}},
		{name: "ssh descendant", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/home/dba/.ssh/cache"}}},
		{name: "disk nested", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/data/src"}}},
		{name: "duplicate tag", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/src"}, {Tag: "farrow-0123456789abcdef0123", Guest: "/work"}}},
		{name: "duplicate guest", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/src"}, {Tag: "farrow-fedcba9876543210fedc", Guest: "/src"}}},
		{name: "nested guests", shares: []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/src"}, {Tag: "farrow-fedcba9876543210fedc", Guest: "/src/nested"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testInput()
			input.Shares = test.shares
			if _, err := Render(input); err == nil {
				t.Fatal("unsafe share contract was accepted")
			}
		})
	}
	input := testInput()
	input.Disks[0].Mount = "/data/nested"
	input.Shares = []Share{{Tag: "farrow-0123456789abcdef0123", Guest: "/data"}}
	if _, err := Render(input); err == nil {
		t.Fatal("share ancestor of a data disk mount was accepted")
	}
}

func TestRenderRejectsMismatchedControlKey(t *testing.T) {
	t.Parallel()
	input := testInput()
	input.Private = &PrivateNetwork{MAC: "02:aa:bb:cc:dd:ee", Address: "10.10.10.10", Prefix: 24, HostAddress: "10.10.10.1"}
	input.Hosts = []Host{{Name: "meta", Address: "10.10.10.10"}, {Name: "node-1", Address: "10.10.10.11"}}
	input.Control = true
	_, input.PrivateKey = testKeyPair(t)
	if _, err := Render(input); err == nil {
		t.Fatal("mismatched control private key was accepted")
	}
}

func TestDiskScriptPublishesFstabBeforeMount(t *testing.T) {
	t.Parallel()
	script := renderDiskScript([]Disk{{Serial: "abcde234567abcde2345", Mount: "/data", Filesystem: "auto"}})
	fstabIndex := strings.Index(script, `install -o root -g root -m 0644 "${fstab_tmp}" /etc/fstab`)
	mountIndex := strings.Index(script, `mount "${mountpoint}"`)
	if fstabIndex < 0 || mountIndex <= fstabIndex {
		t.Fatalf("disk script mounts before publishing fstab:\n%s", script)
	}
}

func TestBuildISORoundTrip(t *testing.T) {
	t.Parallel()
	files, err := Render(testInput())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "seed.iso")
	if err := BuildISO(target, files); err != nil {
		t.Fatalf("BuildISO: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("seed mode = %o, want 600", info.Mode().Perm())
	}
	label, contents, err := ReadISO(target)
	if err != nil {
		t.Fatalf("ReadISO: %v", err)
	}
	if label != "CIDATA" {
		t.Fatalf("volume label = %q, want CIDATA", label)
	}
	for name, want := range files.entries() {
		if !bytes.Equal(contents[name], want) {
			t.Errorf("round-trip mismatch for %s", name)
		}
	}
}

func TestBuildISORejectsExistingTarget(t *testing.T) {
	t.Parallel()
	files, err := Render(testInput())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "seed.iso")
	if err := os.WriteFile(target, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := BuildISO(target, files); err == nil {
		t.Fatal("existing target unexpectedly overwritten")
	}
}

package private

import (
	"testing"

	linuxnet "github.com/pgsty/piglet/internal/network/linux"
	"github.com/pgsty/piglet/internal/platform"
)

func TestSelectDarwinBackendUsesFDForOldOrMissingStream(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		version platform.Version
		help    string
		wantFD  bool
	}{
		{platform.Version{Major: 8, Minor: 2, Patch: 1}, "user stream socket", true},
		{platform.Version{Major: 10, Minor: 1}, "user stream socket", true},
		{platform.Version{Major: 10, Minor: 2}, "user socket", true},
		{platform.Version{Major: 10, Minor: 2}, "user stream socket", false},
		{platform.Version{Major: 11, Minor: 1}, "user stream socket", false},
	} {
		backend := selectDarwinBackend(test.version, test.help, "/private/var/run/piglet-vmnet.sock")
		if backend.DarwinUseFD != test.wantFD || (test.wantFD && backend.ReconnectMS != 0) || (!test.wantFD && backend.ReconnectMS != 1000) {
			t.Errorf("version=%s help=%q backend=%#v", test.version, test.help, backend)
		}
	}
}

func TestLinuxHelperPolicySupportsDebianAndRPMContracts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		family linuxnet.Family
		mode   uint32
		group  string
		ok     bool
	}{
		{linuxnet.Debian, 0o4750, "kvm", true},
		{linuxnet.Debian, 0o4755, "root", false},
		{linuxnet.RPM, 0o4755, "root", true},
		{linuxnet.RPM, 0o4755, "kvm", false},
		{linuxnet.RPM, 0o4750, "kvm", false},
	} {
		err := validateLinuxHelperPolicy(test.family, test.mode, test.group)
		if (err == nil) != test.ok {
			t.Errorf("family=%s mode=%04o group=%s err=%v", test.family, test.mode, test.group, err)
		}
	}
	for input, want := range map[string]linuxnet.Family{
		"ID=ubuntu\nID_LIKE=debian\n":         linuxnet.Debian,
		"ID=rocky\nID_LIKE=\"rhel fedora\"\n": linuxnet.RPM,
	} {
		family, err := linuxHostFamily([]byte(input))
		if err != nil || family != want {
			t.Errorf("family(%q)=%q, %v", input, family, err)
		}
	}
}

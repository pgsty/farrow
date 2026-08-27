package setup

import (
	"errors"
	"reflect"
	"testing"
)

func fakeProbe(goos, goarch, osRelease string, commands []string, files []string) DependencyProbe {
	commandPaths := make(map[string]string, len(commands))
	for _, command := range commands {
		if command != "" && command[0] == '/' {
			commandPaths[command] = command
		} else {
			commandPaths[command] = "/usr/bin/" + command
		}
	}
	regular := make(map[string]bool, len(files))
	for _, path := range files {
		regular[path] = true
	}
	return DependencyProbe{
		GOOS: goos, GOARCH: goarch,
		LookPath: func(name string) (string, error) {
			if path, ok := commandPaths[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		ReadFile: func(path string) ([]byte, error) {
			if path != "/etc/os-release" {
				return nil, errors.New("unexpected file")
			}
			return []byte(osRelease), nil
		},
		RegularFile: func(path string) bool { return regular[path] },
		RootCommand: func(name string) (string, error) {
			if path, ok := commandPaths[name]; ok && (path == "/usr/bin/apt-get" || path == "/usr/bin/dnf") {
				return path, nil
			}
			return "", errors.New("fixed root command not found")
		},
	}
}

func TestPlanDependenciesReadyPrivateDebian(t *testing.T) {
	t.Parallel()
	probe := fakeProbe("linux", "amd64", "ID=ubuntu\nID_LIKE=debian\n",
		[]string{"qemu-system-x86_64", "qemu-img", "ssh", "ssh-keygen", "ip", "systemctl"},
		[]string{
			"/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd",
			"/usr/lib/qemu/qemu-bridge-helper", "/usr/lib/systemd/systemd-networkd",
		})
	plan, err := PlanDependencies(probe, true)
	if err != nil || !plan.Ready || len(plan.Commands) != 0 || len(plan.Missing) != 0 {
		t.Fatalf("private ready plan = %#v, %v", plan, err)
	}
}

func TestPlanDependenciesDebianCommandsAreArchitectureSpecific(t *testing.T) {
	t.Parallel()
	probe := fakeProbe("linux", "arm64", "ID=debian\n", []string{"apt-get"}, nil)
	plan, err := PlanDependencies(probe, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || plan.Manager != "apt" || len(plan.Commands) != 2 {
		t.Fatalf("Debian install plan = %#v", plan)
	}
	want := []string{"install", "-y", "qemu-system-arm", "qemu-utils", "qemu-efi-aarch64", "openssh-client", "iproute2", "systemd"}
	if !reflect.DeepEqual(plan.Commands[1].Args, want) || !plan.Commands[1].Root {
		t.Fatalf("apt install = %#v, want %#v", plan.Commands[1], want)
	}
}

func TestPlanDependenciesUsesControlledRHELQEMUForQuick(t *testing.T) {
	t.Parallel()
	probe := fakeProbe("linux", "amd64", "ID=rocky\nID_LIKE=\"rhel fedora\"\n",
		[]string{"/usr/libexec/qemu-kvm", "qemu-img", "ssh", "ssh-keygen"},
		[]string{"/usr/share/edk2/ovmf/OVMF_CODE.fd", "/usr/share/edk2/ovmf/OVMF_VARS.fd"})
	plan, err := PlanDependencies(probe, false)
	if err != nil || !plan.Ready {
		t.Fatalf("RHEL quick plan = %#v, %v", plan, err)
	}
}

func TestPlanDependenciesSupportsRHELPrivateViaNetworkManager(t *testing.T) {
	t.Parallel()
	probe := fakeProbe("linux", "amd64", "ID=rocky\nID_LIKE=rhel\n", []string{"dnf"}, nil)
	plan, err := PlanDependencies(probe, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Unsupported || plan.Manager != "dnf" || len(plan.Commands) != 1 {
		t.Fatalf("RHEL private plan = %#v", plan)
	}
	for _, argument := range plan.Commands[0].Args {
		if argument == "systemd-networkd" {
			t.Fatalf("RHEL private plan must not install systemd-networkd: %#v", plan.Commands)
		}
	}
}

func TestPlanDependenciesDarwinWithoutHomebrewHasOneResolution(t *testing.T) {
	t.Parallel()
	plan, err := PlanDependencies(fakeProbe("darwin", "arm64", "", nil, nil), false)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Unsupported || !contains(plan.Resolution, "https://brew.sh") {
		t.Fatalf("Darwin plan = %#v", plan)
	}
}

func TestCommandValidateRequiresAbsoluteBinary(t *testing.T) {
	t.Parallel()
	if err := (Command{Name: "install", Binary: "apt-get"}).Validate(); err == nil {
		t.Fatal("relative package-manager binary accepted")
	}
	if err := (Command{Name: "install", Binary: "/usr/bin/apt-get", Args: []string{"install", "qemu"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanDependenciesDoesNotUsePATHForRootPackageManager(t *testing.T) {
	t.Parallel()
	probe := fakeProbe("linux", "amd64", "ID=debian\n", []string{"apt-get"}, nil)
	probe.LookPath = func(name string) (string, error) {
		if name == "apt-get" {
			return "/tmp/attacker/apt-get", nil
		}
		return "", errors.New("not found")
	}
	probe.RootCommand = func(string) (string, error) { return "", errors.New("fixed system path missing") }
	if _, err := PlanDependencies(probe, false); err == nil || !contains(err.Error(), "fixed system path missing") {
		t.Fatalf("PATH package manager was accepted: %v", err)
	}
}

func TestRootCommandTargetAllowlist(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		canonical string
		want      bool
	}{
		{name: "apt-get", canonical: "/usr/bin/apt-get", want: true},
		{name: "dnf", canonical: "/usr/bin/dnf-3", want: true},
		{name: "dnf", canonical: "/usr/bin/dnf5", want: true},
		{name: "dnf", canonical: "/tmp/dnf", want: false},
		{name: "dnf", canonical: "/usr/local/bin/dnf5", want: false},
	} {
		if got := rootCommandTargetAllowed(test.name, test.canonical); got != test.want {
			t.Errorf("rootCommandTargetAllowed(%q, %q) = %t, want %t", test.name, test.canonical, got, test.want)
		}
	}
}

func contains(value, needle string) bool {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}

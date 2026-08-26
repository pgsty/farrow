package preflight

import (
	"net/netip"
	"testing"

	"github.com/pgsty/farrow/internal/network/subnet"
)

func mustPrefix(value string) netip.Prefix { return netip.MustParsePrefix(value) }

func TestEvaluateCleanAbsentAndCustomWarning(t *testing.T) {
	layout, _ := subnet.Parse("172.31.251.0/24")
	report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Install, Layout: layout}, Snapshot{Installation: Installation{Status: "absent"}})
	if !report.Ready || report.ExitCode != 0 || len(report.Findings) != 1 || report.Findings[0].Code != "network.non_default" {
		t.Fatalf("report=%#v", report)
	}
}

func TestEvaluateBroadRouteAndInterfaceOverlap(t *testing.T) {
	layout := subnet.Default()
	report := Evaluate(Request{OS: "linux", Arch: "amd64", Purpose: Install, Layout: layout}, Snapshot{
		Installation: Installation{Status: "absent"},
		Routes:       []Route{{Prefix: mustPrefix("10.0.0.0/8"), Interface: "tun0", Evidence: "10.0.0.0/8 dev tun0"}},
		Interfaces:   []InterfaceAddress{{Prefix: mustPrefix("10.10.10.99/32"), Interface: "eth1", Evidence: "10.10.10.99/32"}},
	})
	if report.Ready || report.ExitCode != 6 {
		t.Fatalf("report=%#v", report)
	}
}

func TestEvaluateExactInstalledAndMismatch(t *testing.T) {
	layout := subnet.Default()
	exact := Snapshot{
		Installation: Installation{Status: "exact", CIDR: layout.CIDR(), HostAddress: layout.HostAddress(), Interface: "bridge100", Healthy: true},
		Routes: []Route{
			{Prefix: layout.Prefix(), Interface: "bridge100", Evidence: "owned"},
			{Prefix: mustPrefix("10.10.10.1/32"), Interface: "lo0", Evidence: "owned host local route"},
		},
		Interfaces: []InterfaceAddress{{Address: netip.MustParseAddr("10.10.10.1"), Prefix: mustPrefix("10.10.10.1/24"), Interface: "bridge100", Evidence: "owned"}},
	}
	if report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Use, Layout: layout}, exact); !report.Ready {
		t.Fatalf("exact report=%#v", report)
	}
	protected := exact
	protected.Installation.Status = "protected"
	if report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Use, Layout: layout}, protected); !report.Ready {
		t.Fatalf("protected report=%#v", report)
	}
	protected.Installation.Healthy = false
	protected.Installation.Problem = "runtime socket is absent"
	if report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Use, Layout: layout}, protected); report.Ready || report.ExitCode != 3 {
		t.Fatalf("protected not-ready report=%#v", report)
	}
	custom, _ := subnet.Parse("172.31.251.0/24")
	if report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Use, Layout: custom}, exact); report.ExitCode != 6 {
		// State mismatch is present, but the existing interface/route also
		// overlaps only its own default subnet, not the custom request. Exit 4.
		if report.ExitCode != 4 {
			t.Fatalf("mismatch report=%#v", report)
		}
	}
}

func TestEvaluateInstalledRequiresOnlyExactOwnedRouteAndAddress(t *testing.T) {
	layout := subnet.Default()
	installation := Installation{Status: "exact", CIDR: layout.CIDR(), HostAddress: layout.HostAddress(), Interface: "farrow0", Healthy: true}
	missing := Evaluate(Request{OS: "linux", Arch: "amd64", Purpose: Use, Layout: layout}, Snapshot{
		Installation: installation,
		Interfaces:   []InterfaceAddress{{Address: netip.MustParseAddr("10.10.10.1"), Prefix: mustPrefix("10.10.10.1/24"), Interface: "farrow0", Evidence: "owned"}},
	})
	if missing.Ready || missing.ExitCode != 3 {
		t.Fatalf("missing exact route report=%#v", missing)
	}
	blackhole := Evaluate(Request{OS: "linux", Arch: "amd64", Purpose: Use, Layout: layout}, Snapshot{
		Installation: installation,
		Routes:       []Route{{Prefix: layout.Prefix(), Interface: "farrow0", Kind: "blackhole", Evidence: "blackhole 10.10.10.0/24"}},
		Interfaces:   []InterfaceAddress{{Address: netip.MustParseAddr("10.10.10.1"), Prefix: mustPrefix("10.10.10.1/24"), Interface: "farrow0", Evidence: "owned"}},
	})
	if blackhole.Ready || blackhole.ExitCode != 6 {
		t.Fatalf("blackhole exact route report=%#v", blackhole)
	}

	conflict := Evaluate(Request{OS: "linux", Arch: "amd64", Purpose: Use, Layout: layout}, Snapshot{
		Installation: installation,
		Routes: []Route{
			{Prefix: layout.Prefix(), Interface: "farrow0", Evidence: "owned exact route"},
			{Prefix: mustPrefix("10.0.0.0/8"), Interface: "farrow0", Evidence: "unexpected broad route"},
		},
		Interfaces: []InterfaceAddress{
			{Address: netip.MustParseAddr("10.10.10.1"), Prefix: mustPrefix("10.10.10.1/24"), Interface: "farrow0", Evidence: "owned exact address"},
			{Address: netip.MustParseAddr("10.10.10.2"), Prefix: mustPrefix("10.10.10.2/24"), Interface: "farrow0", Evidence: "unexpected extra address"},
		},
	})
	if conflict.Ready || conflict.ExitCode != 6 {
		t.Fatalf("broad same-interface conflict report=%#v", conflict)
	}
}

func TestEvaluateDarwinSharingBusyIsResourceConflict(t *testing.T) {
	report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Install, Layout: subnet.Default()}, Snapshot{
		Installation: Installation{Status: "absent"},
		SharingBusy:  "com.apple.NetworkSharing is active for 10.10.10.0/24",
	})
	if report.Ready || report.ExitCode != 6 {
		t.Fatalf("sharing report=%#v", report)
	}
}

func TestEvaluateIntegrityWins(t *testing.T) {
	report := Evaluate(Request{OS: "darwin", Arch: "arm64", Purpose: Use, Layout: subnet.Default()}, Snapshot{
		Installation: Installation{Status: "partial", Problem: "plist without state"},
		Routes:       []Route{{Prefix: mustPrefix("10.10.10.0/24"), Interface: "utun9", Evidence: "conflict"}},
	})
	if report.ExitCode != 7 || report.Ready {
		t.Fatalf("report=%#v", report)
	}
}

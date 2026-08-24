package m0

import (
	"strings"
	"testing"
)

func TestDiagnosticPrivateAddresses(t *testing.T) {
	network, host, meta, peer, err := diagnosticPrivateAddresses("172.31.251.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if network != "172.31.251.0/24" || host != "172.31.251.1" || meta != "172.31.251.10" || peer != "172.31.251.11" {
		t.Fatalf("unexpected addresses: %q %q %q %q", network, host, meta, peer)
	}
	for _, invalid := range []string{"172.31.251.1/24", "172.31.251.0/25", "2001:db8::/64", "invalid"} {
		if _, _, _, _, err := diagnosticPrivateAddresses(invalid); err == nil {
			t.Fatalf("expected %q to fail", invalid)
		}
	}
}

func TestPrivateSSHArgsQuoteKnownHostsPathList(t *testing.T) {
	t.Parallel()
	joined := strings.Join(privateSSHArgs("/key", "/tmp/Application Support/known_hosts", "10.10.10.10", "true"), " ")
	if !strings.Contains(joined, `UserKnownHostsFile="/tmp/Application Support/known_hosts"`) {
		t.Fatalf("private SSH args split known_hosts path: %s", joined)
	}
}

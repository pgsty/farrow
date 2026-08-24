package doctor

import "testing"

func TestReadableNetworkInstallationIncludesProtectedState(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]bool{
		"exact": true, "protected": true, "absent": false, "partial": false, "invalid": false,
	} {
		if got := readableNetworkInstallation(status); got != want {
			t.Errorf("status=%q got=%t want=%t", status, got, want)
		}
	}
}

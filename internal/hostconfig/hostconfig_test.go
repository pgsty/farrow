package hostconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const (
	projectOne = "11111111-1111-4111-8111-111111111111"
	projectTwo = "22222222-2222-4222-8222-222222222222"
)

func fixtureEntries() []Entry {
	return []Entry{
		{Address: "10.10.10.10", Names: []string{"meta", "admin.example.test"}},
		{Address: "10.10.10.11", Names: []string{"node-1"}},
	}
}

func TestReconcileInstallUpdateUninstallPreservesUnownedBytes(t *testing.T) {
	t.Parallel()
	legacy := "127.0.0.1 localhost\n10.9.9.9 legacy # pigsty dns\n"
	otherBlock, _, err := renderBlock(projectTwo, []Entry{{Address: "10.10.10.20", Names: []string{"other"}}})
	if err != nil {
		t.Fatal(err)
	}
	before := []byte(legacy + string(otherBlock))
	after, lines, changed, err := ReconcileContent(before, projectOne, ActionInstall, fixtureEntries())
	if err != nil || !changed || len(lines) != 2 {
		t.Fatalf("install changed=%t lines=%v err=%v", changed, lines, err)
	}
	if !strings.HasPrefix(string(after), string(before)) || !strings.Contains(string(after), "# piglet:"+projectOne+":begin") {
		t.Fatalf("install did not preserve prefix or add block:\n%s", after)
	}
	idempotent, _, changed, err := ReconcileContent(after, projectOne, ActionInstall, fixtureEntries())
	if err != nil || changed || string(idempotent) != string(after) {
		t.Fatalf("idempotent install changed=%t err=%v", changed, err)
	}
	updatedEntries := fixtureEntries()
	updatedEntries[0].Names = append(updatedEntries[0].Names, "grafana.example.test")
	updated, _, changed, err := ReconcileContent(after, projectOne, ActionInstall, updatedEntries)
	if err != nil || !changed || !strings.Contains(string(updated), "grafana.example.test") {
		t.Fatalf("update changed=%t err=%v data=%s", changed, err, updated)
	}
	removed, _, changed, err := ReconcileContent(updated, projectOne, ActionUninstall, nil)
	if err != nil || !changed || string(removed) != string(before) {
		t.Fatalf("uninstall changed=%t err=%v\nwant=%q\ngot =%q", changed, err, before, removed)
	}
}

func TestReconcileRejectsMalformedMarkersAndConflictingNames(t *testing.T) {
	t.Parallel()
	malformed := []byte("127.0.0.1 localhost\n# piglet:" + projectOne + ":begin\n10.10.10.10 meta\n")
	if _, _, _, err := ReconcileContent(malformed, projectOne, ActionInstall, fixtureEntries()); err == nil {
		t.Fatal("unterminated marker block was accepted")
	}
	entries := fixtureEntries()
	entries[1].Names = []string{"meta"}
	if _, _, _, err := ReconcileContent([]byte("127.0.0.1 localhost\n"), projectOne, ActionInstall, entries); err == nil {
		t.Fatal("one hostname mapped to multiple addresses")
	}
	entries = fixtureEntries()
	entries[0].Names = []string{"bad host"}
	if _, _, _, err := ReconcileContent([]byte("127.0.0.1 localhost\n"), projectOne, ActionInstall, entries); err == nil {
		t.Fatal("unsafe hostname was accepted")
	}
	entries = fixtureEntries()
	entries[0].Address = "192.0.2.10"
	if _, _, _, err := ReconcileContent([]byte("127.0.0.1 localhost\n"), projectOne, ActionInstall, entries); err == nil {
		t.Fatal("non-private address was accepted")
	}
}

func TestReconcileRejectsNameConflictOutsideOwnedBlock(t *testing.T) {
	t.Parallel()
	entries := fixtureEntries()
	for _, before := range [][]byte{
		[]byte("127.0.0.1 localhost\n192.0.2.50 meta # user managed\n"),
		func() []byte {
			block, _, err := renderBlock(projectTwo, []Entry{{Address: "10.10.10.20", Names: []string{"meta"}}})
			if err != nil {
				t.Fatal(err)
			}
			return append([]byte("127.0.0.1 localhost\n"), block...)
		}(),
	} {
		if _, _, _, err := ReconcileContent(before, projectOne, ActionInstall, entries); err == nil {
			t.Fatalf("conflicting existing name was accepted:\n%s", before)
		}
	}
	same := []byte("127.0.0.1 localhost\n10.10.10.10 meta\n")
	if _, _, _, err := ReconcileContent(same, projectOne, ActionInstall, entries); err != nil {
		t.Fatalf("same-address existing name should be unambiguous: %v", err)
	}
}

func TestApplyHelperIsDigestBoundAtomicAndPreservesMetadata(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "hosts")
	before := []byte("127.0.0.1 localhost\n10.9.9.9 legacy # pigsty dns\n")
	if err := os.WriteFile(target, before, 0o644); err != nil {
		t.Fatal(err)
	}
	xattrName := "user.piglet-test"
	xattrValue := []byte("preserve-me")
	if err := unix.Setxattr(target, xattrName, xattrValue, 0); err != nil {
		// Darwin reserves the user namespace differently; use a safe test name.
		xattrName = "com.pgsty.piglet.test"
		if err := unix.Setxattr(target, xattrName, xattrValue, 0); err != nil {
			t.Skipf("test filesystem has no writable extended attributes: %v", err)
		}
	}
	after, _, _, err := ReconcileContent(before, projectOne, ActionInstall, fixtureEntries())
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(directory, "staging")
	if err := os.WriteFile(staging, after, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyHelper(target, staging, projectOne, ActionInstall, digest(before), digest(after), false); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != string(after) {
		t.Fatalf("applied hosts mismatch: %v\n%s", err, actual)
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("target mode = %v, %v", info, err)
	}
	value := make([]byte, 64)
	written, err := unix.Getxattr(target, xattrName, value)
	if err != nil || string(value[:written]) != string(xattrValue) {
		t.Fatalf("target xattr = %q, %v", value[:written], err)
	}
}

func TestApplyHelperRefusesStaleTargetAndSymlink(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "hosts")
	before := []byte("127.0.0.1 localhost\n")
	if err := os.WriteFile(target, before, 0o644); err != nil {
		t.Fatal(err)
	}
	after, _, _, err := ReconcileContent(before, projectOne, ActionInstall, fixtureEntries())
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(directory, "staging")
	if err := os.WriteFile(staging, after, 0o600); err != nil {
		t.Fatal(err)
	}
	changed := []byte("127.0.0.1 localhost\n192.0.2.1 concurrent\n")
	if err := os.WriteFile(target, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyHelper(target, staging, projectOne, ActionInstall, digest(before), digest(after), false); err == nil {
		t.Fatal("stale reviewed digest was accepted")
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != string(changed) {
		t.Fatalf("stale apply changed target: %q", actual)
	}
	link := filepath.Join(directory, "hosts-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ApplyHelper(link, staging, projectOne, ActionInstall, digest(changed), digest(after), false); err == nil {
		t.Fatal("symlink target was accepted")
	}
}

func TestValidateTransitionRejectsChangesOutsideOwnedBlock(t *testing.T) {
	t.Parallel()
	before := []byte("127.0.0.1 localhost\n")
	after, _, _, err := ReconcileContent(before, projectOne, ActionInstall, fixtureEntries())
	if err != nil {
		t.Fatal(err)
	}
	after = append([]byte("192.0.2.9 injected\n"), after...)
	if err := validateTransition(before, after, projectOne, ActionInstall); err == nil {
		t.Fatal("transition changed unowned bytes")
	}
}

func TestValidateTransitionRechecksCrossProjectNameConflicts(t *testing.T) {
	t.Parallel()
	other, _, err := renderBlock(projectTwo, []Entry{{Address: "10.10.10.20", Names: []string{"meta"}}})
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte("127.0.0.1 localhost\n"), other...)
	owned, _, err := renderBlock(projectOne, fixtureEntries())
	if err != nil {
		t.Fatal(err)
	}
	after := append(append([]byte(nil), before...), owned...)
	if err := validateTransition(before, after, projectOne, ActionInstall); err == nil {
		t.Fatal("privileged transition accepted a cross-project hostname conflict")
	}
}

func TestInstalledHelperValidationRejectsUserOwnedAndSymlinkPaths(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	helper := filepath.Join(directory, "piglet-hosts-helper")
	if err := os.WriteFile(helper, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateInstalledHelper(helper); err == nil {
		t.Fatal("user-owned helper was accepted for privileged execution")
	}
	link := filepath.Join(directory, "helper-link")
	if err := os.Symlink(helper, link); err != nil {
		t.Fatal(err)
	}
	if err := validateInstalledHelper(link); err == nil {
		t.Fatal("symlink helper was accepted for privileged execution")
	}
	if err := validateInstalledHelper(filepath.Join(directory, "..", filepath.Base(directory), "piglet-hosts-helper")); err == nil {
		t.Fatal("non-canonical helper path was accepted")
	}
}

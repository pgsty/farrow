package main

import (
	"os"
	"strings"
	"testing"

	"github.com/pgsty/farrow/internal/hostconfig"
)

func validArguments() []string {
	target, _ := hostconfig.NativePath()
	return []string{
		"--target", target,
		"--staging", "/tmp/farrow-hosts-helper-test",
		"--project-id", "11111111-1111-4111-8111-111111111111",
		"--action", hostconfig.ActionInstall,
		"--before-sha256", strings.Repeat("a", 64),
		"--after-sha256", strings.Repeat("b", 64),
	}
}

func TestHelperRejectsNonNativeAndRelativeInputs(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--target", "/tmp/not-hosts", "--staging", "/tmp/stage"},
		{"--target", func() string { value, _ := hostconfig.NativePath(); return value }(), "--staging", "relative"},
		append(validArguments(), "extra"),
	} {
		if err := run(args); err == nil {
			t.Errorf("unsafe helper arguments were accepted: %v", args)
		}
	}
}

func TestHelperCannotApplyWithoutRoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("non-root rejection test must not touch the native hosts lock as root")
	}
	if err := run(validArguments()); err == nil {
		t.Fatal("helper unexpectedly accepted an unusable apply")
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCobraRootAndContextualHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("root help code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"Configuration-aware commands", "Examples:", "farrow setup", "Setup:", "Lifecycle:", "Guest Access:", "ss", "Use \"farrow [command] --help\""} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("root help missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("root help wrote stderr: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "Usage:\n  farrow\n") {
		t.Fatalf("root help contains duplicate bare-command usage:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"status", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("status help code=%d stderr=%s", code, stderr.String())
	}
	for _, irrelevant := range []string{"--cpus", "--data-disk", "--delete-persistent", "--repo"} {
		if strings.Contains(stdout.String(), irrelevant) {
			t.Errorf("status help exposes irrelevant flag %s:\n%s", irrelevant, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "Global Flags:") {
		t.Fatalf("status help lacks global flags:\n%s", stdout.String())
	}
	for _, want := range []string{"Examples:", "applied state", "farrow --json status"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("status help missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRootHelpUsesWorkflowOrder(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("root help code=%d stderr=%s", code, stderr.String())
	}
	help := stdout.String()
	for _, pair := range [][2]string{
		{"\n  setup ", "\n  init "},
		{"\n  plan ", "\n  up "},
		{"\n  up ", "\n  status "},
		{"\n  status ", "\n  destroy "},
		{"\n  ssh ", "\n  hosts "},
		{"\n  doctor ", "\n  network "},
		{"\n  version ", "\n  completion "},
	} {
		first := strings.Index(help, pair[0])
		second := strings.Index(help, pair[1])
		if first < 0 || second < 0 || first >= second {
			t.Errorf("root help order %q before %q not preserved:\n%s", pair[0], pair[1], help)
		}
	}
}

func TestLifecycleInterspersedFlagContracts(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, test := range []struct {
		arguments []string
		command   string
		flag      string
	}{
		{arguments: []string{"destroy", "meta", "--force"}, command: "destroy", flag: "force"},
		{arguments: []string{"up", "meta", "--no-wait"}, command: "up", flag: "no-wait"},
		{arguments: []string{"reload", "meta", "--no-wait"}, command: "reload", flag: "no-wait"},
		{arguments: []string{"restart", "meta", "--no-wait"}, command: "restart", flag: "no-wait"},
	} {
		command, remaining, findErr := root.Find(test.arguments)
		if findErr != nil || command.Name() != test.command {
			t.Fatalf("find %v: command=%v remaining=%v err=%v", test.arguments, command, remaining, findErr)
		}
		if parseErr := command.ParseFlags(remaining); parseErr != nil {
			t.Fatalf("parse %v: %v", test.arguments, parseErr)
		}
		if got := strings.Join(command.Flags().Args(), " "); got != "meta" {
			t.Errorf("%v positional args=%q want meta", test.arguments, got)
		}
		value, valueErr := command.Flags().GetBool(test.flag)
		if valueErr != nil || !value {
			t.Errorf("%v --%s=%t err=%v", test.arguments, test.flag, value, valueErr)
		}
	}
}

func TestCommandAliasesResolveToCanonicalCommands(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, test := range []struct {
		arguments []string
		path      string
	}{
		{arguments: []string{"s"}, path: "farrow setup"},
		{arguments: []string{"i"}, path: "farrow init"},
		{arguments: []string{"v"}, path: "farrow validate"},
		{arguments: []string{"pl"}, path: "farrow plan"},
		{arguments: []string{"rc"}, path: "farrow recreate"},
		{arguments: []string{"st"}, path: "farrow status"},
		{arguments: []string{"de"}, path: "farrow destroy"},
		{arguments: []string{"ex"}, path: "farrow exec"},
		{arguments: []string{"l"}, path: "farrow logs"},
		{arguments: []string{"p"}, path: "farrow provision"},
		{arguments: []string{"sc"}, path: "farrow ssh-config"},
		{arguments: []string{"h"}, path: "farrow hosts"},
		{arguments: []string{"im"}, path: "farrow image"},
		{arguments: []string{"images"}, path: "farrow image"},
		{arguments: []string{"dt"}, path: "farrow doctor"},
		{arguments: []string{"n"}, path: "farrow network"},
		{arguments: []string{"net"}, path: "farrow network"},
		{arguments: []string{"ver"}, path: "farrow version"},
		{arguments: []string{"cp"}, path: "farrow completion"},
		{arguments: []string{"h", "i"}, path: "farrow hosts install"},
		{arguments: []string{"h", "u"}, path: "farrow hosts uninstall"},
		{arguments: []string{"im", "ls"}, path: "farrow image list"},
		{arguments: []string{"im", "in"}, path: "farrow image info"},
		{arguments: []string{"im", "p"}, path: "farrow image pull"},
		{arguments: []string{"im", "pr"}, path: "farrow image prune"},
		{arguments: []string{"im", "sy"}, path: "farrow image sync"},
		{arguments: []string{"im", "i"}, path: "farrow image import"},
		{arguments: []string{"n", "st"}, path: "farrow network status"},
		{arguments: []string{"n", "i"}, path: "farrow network install"},
		{arguments: []string{"n", "u"}, path: "farrow network uninstall"},
	} {
		command, remaining, err := root.Find(test.arguments)
		if err != nil || command == nil || command.CommandPath() != test.path || len(remaining) != 0 {
			t.Errorf("find %v: command=%v remaining=%v err=%v, want %s", test.arguments, command, remaining, err, test.path)
		}
	}
}

func TestCommandAliasesAreUniqueWithinEachScope(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		seen := make(map[string]string)
		for _, child := range parent.Commands() {
			for _, name := range append([]string{child.Name()}, child.Aliases...) {
				if previous, duplicate := seen[name]; duplicate {
					t.Errorf("%s name/alias %q is shared by %s and %s", parent.CommandPath(), name, previous, child.CommandPath())
				} else {
					seen[name] = child.CommandPath()
				}
			}
			walk(child)
		}
	}
	walk(root)
}

func TestMisleadingAliasesAreRejected(t *testing.T) {
	for _, arguments := range [][]string{{"clean"}, {"image", "rm"}} {
		var stdout, stderr bytes.Buffer
		if code := run(arguments, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "unknown command") {
			t.Errorf("run %v code=%d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
	}
}

func TestHaltIsHiddenDeprecatedCompatibilityCommand(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	halt, remaining, err := root.Find([]string{"halt"})
	if err != nil || halt == nil || halt.Name() != "halt" || len(remaining) != 0 {
		t.Fatalf("halt command=%v remaining=%v err=%v", halt, remaining, err)
	}
	if !halt.Hidden || halt.Deprecated == "" {
		t.Fatalf("halt hidden=%t deprecated=%q", halt.Hidden, halt.Deprecated)
	}
}

func TestShortCanonicalAndLifecycleCommandsHaveNoExtraAliases(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, name := range []string{"up", "ssh", "ss", "start", "stop", "restart", "reload"} {
		command, _, err := root.Find([]string{name})
		if err != nil || command == nil {
			t.Fatalf("find %s: command=%v err=%v", name, command, err)
		}
		if len(command.Aliases) != 0 {
			t.Errorf("%s aliases=%v, want none", command.CommandPath(), command.Aliases)
		}
	}
}

func TestOperationalFlagsExposeScopedShorthands(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, test := range []struct {
		path      []string
		flag      string
		shorthand string
	}{
		{path: nil, flag: "verbose", shorthand: "v"},
		{path: []string{"setup"}, flag: "yes", shorthand: "y"},
		{path: []string{"setup"}, flag: "dry-run", shorthand: "d"},
		{path: []string{"setup"}, flag: "mode", shorthand: "m"},
		{path: []string{"up"}, flag: "repo", shorthand: "r"},
		{path: []string{"up"}, flag: "no-wait", shorthand: "n"},
		{path: []string{"up"}, flag: "rollback", shorthand: "b"},
		{path: []string{"destroy"}, flag: "force", shorthand: "f"},
		{path: []string{"provision"}, flag: "script", shorthand: "s"},
		{path: []string{"provision"}, flag: "parallel", shorthand: "p"},
		{path: []string{"provision"}, flag: "timeout", shorthand: "t"},
		{path: []string{"ssh-config"}, flag: "install", shorthand: "i"},
		{path: []string{"ssh-config"}, flag: "remove", shorthand: "r"},
		{path: []string{"ssh-config"}, flag: "name", shorthand: "n"},
		{path: []string{"logs"}, flag: "source", shorthand: "s"},
		{path: []string{"hosts", "install"}, flag: "yes", shorthand: "y"},
		{path: []string{"image", "prune"}, flag: "yes", shorthand: "y"},
		{path: []string{"image", "import"}, flag: "sha256", shorthand: "s"},
		{path: []string{"image", "import"}, flag: "source-user", shorthand: "u"},
		{path: []string{"network", "install"}, flag: "archive", shorthand: "a"},
		{path: []string{"network", "install"}, flag: "interface-id", shorthand: "i"},
		{path: []string{"network", "install"}, flag: "yes", shorthand: "y"},
	} {
		command := root
		var err error
		if len(test.path) != 0 {
			command, _, err = root.Find(test.path)
		}
		if err != nil || command == nil {
			t.Fatalf("find %v: command=%v err=%v", test.path, command, err)
		}
		flag := command.LocalNonPersistentFlags().Lookup(test.flag)
		if test.path == nil {
			flag = command.PersistentFlags().Lookup(test.flag)
		}
		if flag == nil {
			t.Errorf("%s has no --%s flag", command.CommandPath(), test.flag)
			continue
		}
		if flag.Shorthand != test.shorthand {
			t.Errorf("%s --%s shorthand=%q, want %q", command.CommandPath(), test.flag, flag.Shorthand, test.shorthand)
		}
	}

	if root.PersistentFlags().Lookup("json").Shorthand != "" || root.PersistentFlags().Lookup("yaml").Shorthand != "" {
		t.Fatal("JSON and YAML presentation flags unexpectedly gained shorthands")
	}
}

func TestSafetySensitiveFlagsRemainLongOnly(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, test := range []struct {
		path []string
		flag string
	}{
		{path: []string{"recreate"}, flag: "force"},
		{path: []string{"destroy"}, flag: "delete-persistent"},
		{path: []string{"destroy"}, flag: "purge"},
		{path: []string{"provision"}, flag: "sudo"},
		{path: []string{"image", "sync"}, flag: "allow-downgrade"},
	} {
		command, _, err := root.Find(test.path)
		if err != nil {
			t.Fatalf("find %v: %v", test.path, err)
		}
		flag := command.LocalNonPersistentFlags().Lookup(test.flag)
		if flag == nil {
			t.Errorf("%s has no --%s flag", command.CommandPath(), test.flag)
			continue
		}
		if flag.Shorthand != "" {
			t.Errorf("%s --%s shorthand=%q, want none", command.CommandPath(), test.flag, flag.Shorthand)
		}
	}
}

func TestAliasesAreDiscoverableInHelpAndCompletion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("root help code=%d stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Command aliases:", "s=setup", "sc=ssh-config", "dt=doctor", "im=image"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("root help missing %q:\n%s", want, stdout.String())
		}
	}
	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"__complete", "sc"}, want: "sc\tAlias for ssh-config"},
		{arguments: []string{"__complete", "dt"}, want: "dt\tAlias for doctor"},
		{arguments: []string{"__complete", "im", "pr"}, want: "pr\tAlias for prune"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(test.arguments, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), test.want) {
			t.Errorf("completion %v code=%d stdout=%q stderr=%q want=%q", test.arguments, code, stdout.String(), stderr.String(), test.want)
		}
	}
}

func TestCommandTreeDeclaresUserFacingContracts(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	var missingArgs []string
	var missingLong []string
	var missingExample []string
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, command := range parent.Commands() {
			if command.Hidden || command.Name() == "help" {
				continue
			}
			if command.Runnable() && command.Args == nil {
				missingArgs = append(missingArgs, command.CommandPath())
			}
			if strings.TrimSpace(command.Long) == "" {
				missingLong = append(missingLong, command.CommandPath())
			}
			if strings.TrimSpace(command.Example) == "" {
				missingExample = append(missingExample, command.CommandPath())
			}
			walk(command)
		}
	}
	if root.Args == nil {
		missingArgs = append(missingArgs, root.CommandPath())
	}
	if strings.TrimSpace(root.Long) == "" {
		missingLong = append(missingLong, root.CommandPath())
	}
	if strings.TrimSpace(root.Example) == "" {
		missingExample = append(missingExample, root.CommandPath())
	}
	walk(root)
	for _, values := range [][]string{missingArgs, missingLong, missingExample} {
		sort.Strings(values)
	}
	if len(missingArgs) != 0 || len(missingLong) != 0 || len(missingExample) != 0 {
		t.Fatalf("incomplete Cobra contracts:\nargs=%v\nlong=%v\nexamples=%v", missingArgs, missingLong, missingExample)
	}
}

func TestEveryVisibleCommandRendersContextualHelp(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	paths := make([][]string, 0)
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, command := range parent.Commands() {
			if command.Hidden || command.Name() == "help" {
				continue
			}
			parts := strings.Fields(command.CommandPath())
			paths = append(paths, append([]string(nil), parts[1:]...))
			walk(command)
		}
	}
	walk(root)
	for _, path := range paths {
		path := path
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			arguments := append(append([]string(nil), path...), "--help")
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(arguments, &stdout, &stderr); code != exitOK {
				t.Fatalf("run(%v) code=%d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
			}
			for _, want := range []string{"Usage:", "Examples:"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("run(%v) help missing %q:\n%s", arguments, want, stdout.String())
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%v) help wrote stderr: %q", arguments, stderr.String())
			}
		})
	}
}

func TestConfigFlagScopeAndDiscoveryHelp(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	expected := map[string]bool{
		"farrow setup": true, "farrow validate": true,
		"farrow plan": true, "farrow up": true, "farrow reload": true, "farrow recreate": true,
	}
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, command := range parent.Commands() {
			if command.Hidden || command.Name() == "help" {
				continue
			}
			flag := command.LocalNonPersistentFlags().Lookup("file")
			if got, want := flag != nil, expected[command.CommandPath()]; got != want {
				t.Errorf("%s local --file presence=%t want=%t", command.CommandPath(), got, want)
			}
			if flag != nil && !strings.Contains(flag.Usage, "inventory") {
				t.Errorf("%s --file help does not describe an inventory: %q", command.CommandPath(), flag.Usage)
			}
			walk(command)
		}
	}
	walk(root)
}

func TestCIDRAndInitForceUseScopedShorthands(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, test := range []struct {
		path      []string
		flag      string
		shorthand string
	}{
		{path: []string{"setup"}, flag: "cidr", shorthand: "c"},
		{path: []string{"init"}, flag: "cidr", shorthand: "c"},
		{path: []string{"init"}, flag: "force", shorthand: "f"},
		{path: []string{"network", "status"}, flag: "cidr", shorthand: "c"},
		{path: []string{"network", "install"}, flag: "cidr", shorthand: "c"},
	} {
		command, _, err := root.Find(test.path)
		if err != nil {
			t.Fatalf("find %v: %v", test.path, err)
		}
		flag := command.LocalNonPersistentFlags().Lookup(test.flag)
		if flag == nil {
			t.Errorf("%s has no --%s flag", command.CommandPath(), test.flag)
			continue
		}
		if flag.Shorthand != test.shorthand {
			t.Errorf("%s --%s shorthand=%q, want %q", command.CommandPath(), test.flag, flag.Shorthand, test.shorthand)
		}
	}
}

func TestRetiredNetworkCIDRFlagIsRejected(t *testing.T) {
	for _, arguments := range [][]string{
		{"init", "--network-cidr", "10.20.30.0/24"},
		{"setup", "--network-cidr", "10.20.30.0/24", "--dry-run"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(arguments, &stdout, &stderr); code != exitUsage {
			t.Fatalf("run(%v) code=%d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown flag: --network-cidr") {
			t.Fatalf("run(%v) stdout=%q stderr=%q", arguments, stdout.String(), stderr.String())
		}
	}
}

func TestOnlyRemotePassthroughDisablesCobraFlagParsing(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	expected := map[string]bool{"farrow ssh": true, "farrow exec": true}
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, command := range parent.Commands() {
			if command.Hidden || command.Name() == "help" {
				continue
			}
			if got, want := command.DisableFlagParsing, expected[command.CommandPath()]; got != want {
				t.Errorf("%s DisableFlagParsing=%t want=%t", command.CommandPath(), got, want)
			}
			walk(command)
		}
	}
	walk(root)
}

func TestDestructiveForceFlagsExplainConfirmationBoundary(t *testing.T) {
	root := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	for _, path := range [][]string{{"recreate"}, {"destroy"}} {
		command, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		flag := command.LocalNonPersistentFlags().Lookup("force")
		if flag == nil || !strings.Contains(strings.ToLower(flag.Usage), "confirmation") {
			t.Errorf("%s --force help does not explain confirmation: %#v", command.CommandPath(), flag)
		}
	}
}

func TestCobraRejectsExclusiveFlagsBeforeOperations(t *testing.T) {
	for _, arguments := range [][]string{
		{"setup", "--dry-run", "--yes"},
		{"ssh-config", "--install", "--remove"},
		{"image", "prune", "--dry-run", "--yes"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(arguments, &stdout, &stderr); code != exitUsage {
			t.Fatalf("run(%v) code=%d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "none of the others") {
			t.Fatalf("run(%v) stdout=%q stderr=%q", arguments, stdout.String(), stderr.String())
		}
	}
}

func TestCobraRejectsInvalidTypedOptionsBeforeOperations(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"setup", "--mode", "bogus"}, want: "--mode must be one of"},
		{arguments: []string{"logs", "--source", "bogus"}, want: "--source must be one of"},
		{arguments: []string{"provision", "--script", "missing.sh", "--parallel", "0"}, want: "--parallel must be between"},
		{arguments: []string{"ssh-config", "--name", "bad/name"}, want: "invalid SSH config name"},
		{arguments: []string{"ssh-config", "--remove", "meta"}, want: "does not accept node selectors"},
		{arguments: []string{"image", "import", "missing.qcow2", "--name", "local-x", "--boot", "bad", "--source-user", "dba"}, want: "--boot must be one of"},
		{arguments: []string{"image", "info", "d13", "--arch", "s390x"}, want: "--arch must be one of"},
		{arguments: []string{"network", "install", "--mode", "bogus"}, want: "--mode must be one of"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(test.arguments, &stdout, &stderr); code != exitUsage {
			t.Fatalf("run(%v) code=%d stdout=%q stderr=%q", test.arguments, code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
			t.Fatalf("run(%v) stdout=%q stderr=%q want=%q", test.arguments, stdout.String(), stderr.String(), test.want)
		}
	}
}

func TestUnknownCommandsOfferContextualSuggestions(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"statuz"}, want: "status"},
		{arguments: []string{"image", "pul"}, want: "pull"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(test.arguments, &stdout, &stderr); code != exitUsage {
			t.Fatalf("run(%v) code=%d stdout=%q stderr=%q", test.arguments, code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Did you mean this?") || !strings.Contains(stderr.String(), test.want) {
			t.Fatalf("run(%v) stdout=%q stderr=%q", test.arguments, stdout.String(), stderr.String())
		}
	}
}

func TestCommandGroupWithoutSubcommandShowsExamples(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"image"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"Available Commands:", "Examples:", "farrow image pull d13"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("image help missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Usage:\n  farrow image\n") {
		t.Fatalf("namespace help contains duplicate bare-command usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("namespace help wrote stderr: %q", stderr.String())
	}
}

func TestExplicitHelpRemainsHumanReadableInStructuredMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--json", "image", "--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") || !strings.Contains(stdout.String(), "Examples:") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var payload any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err == nil {
		t.Fatal("explicit help unexpectedly rendered as structured data")
	}
}

func TestSSHShortcutDefaultsToFixedFarrowName(t *testing.T) {
	options, err := sshShortcutOptions("")
	if err != nil {
		t.Fatal(err)
	}
	want := sshConfigOptions{Install: true, Name: "farrow"}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("shortcut options=%v want=%v", options, want)
	}
}

func TestSSHShortcutAcceptsExplicitName(t *testing.T) {
	options, err := sshShortcutOptions("dev")
	if err != nil {
		t.Fatal(err)
	}
	want := sshConfigOptions{Install: true, Name: "dev"}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("shortcut options=%v want=%v", options, want)
	}
}

func TestViperOutputEnvironment(t *testing.T) {
	t.Setenv("FARROW_OUTPUT", "json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("version code=%d stderr=%s", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["name"] != "farrow" {
		t.Fatalf("output=%q result=%v err=%v", stdout.String(), result, err)
	}
}

func TestCompletionRejectsUnsupportedShell(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"completion", "nonsense"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("completion code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid argument") || !strings.Contains(stderr.String(), "farrow completion") {
		t.Fatalf("completion stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestStructuredUsageErrorIsParseableAndDiagnostic(t *testing.T) {
	for _, arguments := range [][]string{{"--json"}, {"--json", "image"}, {"--json", "up", "--bogus"}} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(arguments, &stdout, &stderr); code != exitUsage {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			var failure commandFailure
			if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
				t.Fatalf("structured usage output is not JSON: %v\n%s", err, stdout.String())
			}
			if failure.Error != "usage" || failure.Message == "" || !strings.HasPrefix(stderr.String(), "error: ") {
				t.Fatalf("failure=%#v stderr=%q", failure, stderr.String())
			}
		})
	}
}

func TestStructuredBusinessFailureGetsFallbackPayload(t *testing.T) {
	t.Setenv("FARROW_HOME", t.TempDir())
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	for _, test := range []struct {
		arguments []string
		code      int
		category  string
		message   string
	}{
		{arguments: []string{"--json", "logs"}, code: exitConflict, category: "conflict", message: "no deployment state found"},
		{arguments: []string{"--json", "status"}, code: exitConflict, category: "conflict", message: "no deployment state found"},
		{arguments: []string{"--json", "up"}, code: exitConflict, category: "conflict", message: "no configuration found"},
		{arguments: []string{"--json", "validate", "-f", "missing.yml"}, code: exitUsage, category: "usage", message: "missing.yml"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(test.arguments, &stdout, &stderr); code != test.code {
			t.Fatalf("run(%v) code=%d want=%d stdout=%q stderr=%q", test.arguments, code, test.code, stdout.String(), stderr.String())
		}
		var failure commandFailure
		if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
			t.Fatalf("run(%v) invalid JSON failure: %v\n%s", test.arguments, err, stdout.String())
		}
		if failure.Error != test.category || !strings.Contains(failure.Message, test.message) || !strings.HasPrefix(stderr.String(), "error: ") {
			t.Fatalf("run(%v) failure=%#v stderr=%q", test.arguments, failure, stderr.String())
		}
	}
}

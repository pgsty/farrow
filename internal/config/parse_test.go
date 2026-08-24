package config

import (
	"testing"

	"github.com/pgsty/piglet/internal/spec"
)

func TestParseSize(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]int64{"4GiB": 4 * spec.GiB, "64GB": 64_000_000_000, "512MiB": 512 << 20} {
		got, err := ParseSize(input)
		if err != nil || got != expected {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", input, got, err, expected)
		}
	}
	for _, input := range []string{"", "4", "1.5GiB", "-1GiB", "0GiB", "999999999999999999999GiB"} {
		if _, err := ParseSize(input); err == nil {
			t.Errorf("invalid size %q accepted", input)
		}
	}
}

func TestParseForward(t *testing.T) {
	t.Parallel()
	forward, err := ParseForward("15432:5432")
	if err != nil || forward.Bind != "127.0.0.1" || forward.Host != 15432 || forward.Guest != 5432 {
		t.Fatalf("forward = %#v, %v", forward, err)
	}
	forward, err = ParseForward("0.0.0.0:8080:80")
	if err != nil || forward.Bind != "0.0.0.0" {
		t.Fatalf("bound forward = %#v, %v", forward, err)
	}
	for _, input := range []string{"bad", "x:1:2", "0:2", "1:70000"} {
		if _, err := ParseForward(input); err == nil {
			t.Errorf("invalid forward %q accepted", input)
		}
	}
}

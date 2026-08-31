//go:build linux

package process

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseProcStatStartHandlesClosingParenthesisInCommand(t *testing.T) {
	fields := []string{"S"}
	for value := 1; value <= 18; value++ {
		fields = append(fields, fmt.Sprintf("%d", value))
	}
	fields = append(fields, "424242", "0", "0")
	data := []byte("123 (worker ) with spaces) " + strings.Join(fields, " ") + "\n")
	started, err := parseProcStatStart(data)
	if err != nil || started != "procstat:424242" {
		t.Fatalf("parsed start = %q, %v", started, err)
	}
	for _, malformed := range [][]byte{[]byte("123 worker S 1"), []byte("123 (worker) S 1 2"), []byte("123 (worker) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 nope")} {
		if _, err := parseProcStatStart(malformed); err == nil {
			t.Fatalf("malformed proc stat accepted: %q", malformed)
		}
	}
}

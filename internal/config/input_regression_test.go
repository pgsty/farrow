package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestInventoryRejectsLossyNumbers(t *testing.T) {
	for _, value := range []string{"vm_cpu: 1.9", "vm_mem: 1.5", "vm_disk: 17179869248", "vm_mem: 17592186048512", "vm_disks: [{size: 17179869248, path: /data}]"} {
		t.Run(value, func(t *testing.T) {
			data := fmt.Sprintf("all:\n  children:\n    meta:\n      hosts:\n        10.10.10.10: {%s}\n", value)
			if _, err := ParseInventory([]byte(data)); err == nil {
				t.Fatal("lossy numeric input accepted")
			} else if !strings.Contains(err.Error(), "integer") && !strings.Contains(err.Error(), "overflowing") {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

func TestInventoryRejectsTrailingDocuments(t *testing.T) {
	data := "all:\n  hosts:\n    10.10.10.10: {}\n---\nall:\n  hosts:\n    10.10.10.11: {}\n"
	if _, err := ParseInventory([]byte(data)); err == nil || !strings.Contains(err.Error(), "one YAML document") {
		t.Fatalf("parse: %v", err)
	}
	if _, err := DetectFormat([]byte(data)); err == nil {
		t.Fatal("format detection ignored trailing document")
	}
}

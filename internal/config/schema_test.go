package config

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCheckedInSchemaClosesTypedObjects(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../schemas/farrow-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatal("top-level schema must reject unknown fields")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema properties missing")
	}
	for _, required := range []string{"version", "name", "network", "defaults", "ssh", "storage", "nodes"} {
		if _, ok := properties[required]; !ok {
			t.Errorf("schema property %s missing", required)
		}
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema definitions missing")
	}
	for _, name := range []string{"network", "defaults", "ssh", "storage", "disk", "share", "forward", "node"} {
		definition, ok := definitions[name].(map[string]any)
		if !ok || definition["additionalProperties"] != false {
			t.Errorf("schema definition %s does not reject unknown fields", name)
		}
	}
	forward, ok := definitions["forward"].(map[string]any)
	if !ok {
		t.Fatal("forward schema definition missing")
	}
	forwardProperties, ok := forward["properties"].(map[string]any)
	if !ok {
		t.Fatal("forward schema properties missing")
	}
	bind, ok := forwardProperties["bind"].(map[string]any)
	if !ok || bind["format"] != "ipv4" {
		t.Fatalf("forward bind schema must be IPv4, got %#v", bind)
	}
	if _, ok := forwardProperties["requested_host"]; ok {
		t.Fatal("resolved-only requested_host evidence must not be accepted as user configuration")
	}
	share, ok := definitions["share"].(map[string]any)
	if !ok {
		t.Fatal("share schema definition missing")
	}
	required, ok := share["required"].([]any)
	if !ok || len(required) != 2 || required[0] != "host" || required[1] != "guest" {
		t.Fatalf("share required fields = %#v", share["required"])
	}
	shareProperties, ok := share["properties"].(map[string]any)
	if !ok {
		t.Fatal("share schema properties missing")
	}
	readonly, ok := shareProperties["readonly"].(map[string]any)
	if !ok || readonly["type"] != "boolean" || readonly["default"] != true {
		t.Fatalf("share readonly schema must default true, got %#v", readonly)
	}
	if _, ok := shareProperties["tag"]; ok {
		t.Fatal("derived share tag must not be accepted as user configuration")
	}
	node, ok := definitions["node"].(map[string]any)
	if !ok {
		t.Fatal("node schema definition missing")
	}
	nodeProperties, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatal("node schema properties missing")
	}
	shares, ok := nodeProperties["shares"].(map[string]any)
	if !ok || shares["type"] != "array" || shares["maxItems"] != float64(8) {
		t.Fatalf("node shares schema mismatch: %#v", shares)
	}
}

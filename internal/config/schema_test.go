package config

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCheckedInSchemaClosesTypedObjects(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../schemas/piglet-v1.schema.json")
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
	for _, name := range []string{"network", "defaults", "ssh", "storage", "disk", "forward", "node"} {
		definition, ok := definitions[name].(map[string]any)
		if !ok || definition["additionalProperties"] != false {
			t.Errorf("schema definition %s does not reject unknown fields", name)
		}
	}
}

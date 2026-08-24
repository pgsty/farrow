package diagnostics

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRedactTextSecretForms(t *testing.T) {
	t.Parallel()
	canary := "PIGLET_SECRET_CANARY_4f34d3a1"
	input := []byte("Authorization: Bearer " + canary + "\n" +
		"--token=" + canary + "\n" +
		"url=https://user:" + canary + "@example.invalid/path?access_token=" + canary + "\n" +
		"-----BEGIN OPENSSH PRIVATE KEY-----\n" + canary + "\n-----END OPENSSH PRIVATE KEY-----\n")
	output := RedactText(input)
	if bytes.Contains(output, []byte(canary)) || bytes.Contains(output, []byte("OPENSSH PRIVATE KEY")) {
		t.Fatalf("redaction leaked canary/private block:\n%s", output)
	}
	if bytes.Count(output, []byte(redacted)) < 4 {
		t.Fatalf("expected multiple redactions:\n%s", output)
	}
}

func TestRedactJSONPreservesShape(t *testing.T) {
	t.Parallel()
	canary := "PIGLET_SECRET_CANARY_59f0c7c2"
	input := []byte(`{"node":"meta","nested":{"api_token":"` + canary + `","public_key":"ssh-ed25519 safe"},"args":["--password=` + canary + `"]}`)
	output := RedactJSON(input)
	if bytes.Contains(output, []byte(canary)) {
		t.Fatalf("JSON redaction leaked canary: %s", output)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("redacted JSON is invalid: %v\n%s", err, output)
	}
	if decoded["node"] != "meta" {
		t.Fatalf("non-secret JSON field changed: %#v", decoded)
	}
}

// Package diagnostics builds explicitly allowlisted, secret-redacted support
// bundles for the current Piglet project.
package diagnostics

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var (
	sensitiveLine = regexp.MustCompile(`(?im)^([^\r\n]*(?:authorization|proxy-authorization|password|passwd|passphrase|access[_-]?token|refresh[_-]?token|api[_-]?key|private[_-]?key|client[_-]?secret|credential|set-cookie|token|secret)[^\r\n]*?[:=]\s*)([^\r\n]*)$`)
	sensitiveFlag = regexp.MustCompile(`(?i)(--(?:password|passwd|passphrase|token|secret|api-key|private-key)(?:=|\s+))([^\s]+)`)
	bearerToken   = regexp.MustCompile(`(?i)(\bBearer\s+)[A-Za-z0-9._~+/=-]+`)
	querySecret   = regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|token|api_key|password)=)[^&\s]+`)
	urlUserInfo   = regexp.MustCompile(`(://)[^/@\s]+:[^/@\s]+@`)
	knownToken    = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16})\b`)
	jwtToken      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
)

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{"authorization", "proxyauthorization", "password", "passwd", "passphrase", "accesstoken", "refreshtoken", "apikey", "privatekey", "clientsecret", "credential", "setcookie"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "token" || normalized == "secret" || strings.HasSuffix(normalized, "token") || strings.HasSuffix(normalized, "secret")
}

func redactPEM(input []byte) []byte {
	lines := bytes.Split(input, []byte("\n"))
	result := make([][]byte, 0, len(lines))
	inPrivateBlock := false
	for _, line := range lines {
		upper := strings.ToUpper(string(line))
		if !inPrivateBlock && strings.Contains(upper, "-----BEGIN ") && (strings.Contains(upper, "PRIVATE KEY-----") || strings.Contains(upper, "CREDENTIAL-----")) {
			result = append(result, []byte(redacted+" PRIVATE BLOCK"))
			inPrivateBlock = true
			continue
		}
		if inPrivateBlock {
			if strings.Contains(upper, "-----END ") {
				inPrivateBlock = false
			}
			continue
		}
		result = append(result, line)
	}
	return bytes.Join(result, []byte("\n"))
}

// RedactText applies conservative line, header, URL, flag, token, and private
// block redaction. It intentionally prefers diagnostic loss over secret loss.
func RedactText(input []byte) []byte {
	output := redactPEM(input)
	output = sensitiveLine.ReplaceAll(output, []byte(`${1}`+redacted))
	output = sensitiveFlag.ReplaceAll(output, []byte(`${1}`+redacted))
	output = bearerToken.ReplaceAll(output, []byte(`${1}`+redacted))
	output = querySecret.ReplaceAll(output, []byte(`${1}`+redacted))
	output = urlUserInfo.ReplaceAll(output, []byte(`${1}`+redacted+`@`))
	output = knownToken.ReplaceAll(output, []byte(redacted))
	output = jwtToken.ReplaceAll(output, []byte(redacted))
	return output
}

func redactJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveKey(key) {
				typed[key] = redacted
			} else {
				typed[key] = redactJSONValue(child)
			}
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = redactJSONValue(typed[index])
		}
		return typed
	case string:
		return string(RedactText([]byte(typed)))
	default:
		return value
	}
}

// RedactJSON preserves valid JSON when possible and falls back to text
// redaction for corrupt state that is still useful in a diagnostic bundle.
func RedactJSON(input []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return RedactText(input)
	}
	value = redactJSONValue(value)
	output, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return RedactText(input)
	}
	return append(output, '\n')
}

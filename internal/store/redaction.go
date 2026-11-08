package store

import (
	"encoding/json"
	"regexp"
	"strings"
	"whirlwind/internal/redaction"
)

// SafeJSON strips credential-bearing protocol fields without collecting process environment.
func SafeJSON(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var data any
	if err = json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	redact(data)
	return json.Marshal(data)
}
func redact(value any) {
	switch v := value.(type) {
	case map[string]any:
		redactObject(v)
	case []any:
		redactArguments(v)
	}
}

func redactObject(v map[string]any) {
	stripPrivateThoughts(v)
	secret, _ := v["isSecret"].(bool)
	marked, _ := v["secret"].(bool)
	for k, x := range v {
		if secretField(strings.ToLower(k), secret || marked) {
			v[k] = "[redacted]"
		} else if text, ok := x.(string); ok {
			v[k] = RedactText(text)
		} else {
			redact(x)
		}
	}
}

func secretField(key string, marked bool) bool {
	switch key {
	case "authorization", "access_token", "accesstoken", "refresh_token", "refreshtoken", "auth_token", "authtoken", "api_key", "apikey", "password", "secret", "client_secret", "clientsecret", "env", "environment":
		return true
	case "proxy_authorization", "proxy-authorization", "http_proxy", "https_proxy", "all_proxy":
		return true
	case "answer", "answers", "text", "content":
		return marked
	default:
		return false
	}
}

func stripPrivateThoughts(v map[string]any) {
	if method, ok := v["method"].(string); ok && strings.Contains(strings.ToLower(method), "/reasoning/") {
		if params, ok := v["params"].(map[string]any); ok {
			for _, key := range []string{"delta", "text", "summary", "content"} {
				delete(params, key)
			}
		}
	}
	private := v["sessionUpdate"] == "agent_thought_chunk"
	switch v["type"] {
	case "thinking", "redacted_thinking", "reasoning":
		private = true
	}
	if private {
		for _, key := range []string{"thinking", "text", "content", "summary", "encrypted_content", "encryptedContent", "raw_content"} {
			delete(v, key)
		}
	}
}

func redactArguments(v []any) {
	for i, x := range v {
		if flag, ok := x.(string); ok {
			redactArgument(v, i, strings.ToLower(flag))
			v[i] = RedactText(v[i].(string))
		}
		redact(x)
	}
}

func redactArgument(v []any, i int, key string) {
	for _, flag := range []string{"--api-key", "--token", "--password", "--secret"} {
		if strings.HasPrefix(key, flag+"=") {
			v[i] = flag + "=[redacted]"
		}
		if key == flag && i+1 < len(v) {
			v[i+1] = "[redacted]"
		}
	}
}

var bearerSecret = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
var assignedSecret = regexp.MustCompile(`(?i)(access_token|refresh_token|api_key|client_secret|password|auth_token)([=:]\s*)[^\s&"']+`)
var proxyURLCredentials = regexp.MustCompile(`(?i)(https?://)[^/\s@]+@`)
var proxyAuthorization = regexp.MustCompile(`(?i)(proxy-authorization\s*[:=]\s*(?:basic|bearer)\s+)[A-Za-z0-9._~+/=-]+`)

// RedactText removes credential literals from otherwise useful provider diagnostics.
func RedactText(text string) string {
	text = bearerSecret.ReplaceAllString(text, "Bearer [redacted]")
	text = assignedSecret.ReplaceAllString(text, "${1}${2}[redacted]")
	text = proxyAuthorization.ReplaceAllString(text, "${1}[redacted]")
	text = proxyURLCredentials.ReplaceAllString(text, "${1}[redacted]@")
	return redaction.RedactText(text)
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

func elicitationRequest(t *testing.T, raw string) (Message, map[string]any) {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	return Message{Method: "mcpServer/elicitation/request", Params: json.RawMessage(raw)}, params
}

func TestNativePermissionResponsesMatchProtocol(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		action := "decline"
		if allowed {
			action = "accept"
		}
		result := permissionResponse("mcpServer/elicitation/request", nil, allowed)
		if !reflect.DeepEqual(result, map[string]any{"action": action}) {
			t.Fatalf("elicitation response: %#v", result)
		}
		requested := map[string]any{"network": map[string]any{"enabled": true}, "fileSystem": nil, "extra": "never grant"}
		result = permissionResponse("item/permissions/requestApproval", map[string]any{"permissions": requested}, allowed)
		want := map[string]any{}
		if allowed {
			want["network"] = map[string]any{"enabled": true}
		}
		if !reflect.DeepEqual(result, map[string]any{"permissions": want, "scope": "turn"}) {
			t.Fatalf("permission response: %#v", result)
		}
	}
}

func TestMCPElicitationURLUsesPermissionHook(t *testing.T) {
	m, params := elicitationRequest(t, `{"mode":"url","serverName":"jenkins","url":"https://ci.example/verify","message":"Verify CI access","elicitationId":"verify-1"}`)
	s := &providerSession{hooks: Hooks{Permission: func(_ context.Context, permission Permission) (bool, error) {
		if permission.Tool != "mcpServer/elicitation/request" || string(permission.Raw) != string(m.Params) {
			t.Fatalf("lost native authentication request: %+v", permission)
		}
		return true, nil
	}}}
	response, err := s.mcpElicitation(context.Background(), m, params)
	if err != nil || !reflect.DeepEqual(response, map[string]any{"action": "accept"}) {
		t.Fatalf("URL permission: %#v %v", response, err)
	}
	s.hooks = Hooks{}
	response, err = s.mcpElicitation(context.Background(), m, params)
	if err != nil || !reflect.DeepEqual(response, map[string]any{"action": "decline"}) {
		t.Fatalf("missing permission hook accepted URL: %#v %v", response, err)
	}
}

func TestMCPElicitationFormConvertsTypedAnswersAndDropsUnknownFields(t *testing.T) {
	m, params := elicitationRequest(t, `{"mode":"form","message":"Choose build","requestedSchema":{"type":"object","properties":{"job":{"type":"string","title":"Job","enum":["unit","integration"]},"retry":{"type":"boolean"},"build":{"type":"integer"},"threshold":{"type":"number"},"note":{"type":"string"}},"required":["job","retry","build","threshold"]}}`)
	s := &providerSession{hooks: Hooks{Questions: func(_ context.Context, questions []model.Question) (map[string]string, error) {
		if len(questions) != 5 {
			t.Fatalf("missing form fields: %+v", questions)
		}
		return map[string]string{"job": "unit", "retry": "true", "build": "42", "threshold": "0.75", "unrequested": "do not send"}, nil
	}}}
	response, err := s.mcpElicitation(context.Background(), m, params)
	encoded, _ := json.Marshal(response)
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	want := map[string]any{"action": "accept", "content": map[string]any{"job": "unit", "retry": true, "build": float64(42), "threshold": 0.75}}
	if err != nil || !reflect.DeepEqual(decoded, want) {
		t.Fatalf("typed form response: %s %v", encoded, err)
	}
}

func TestMCPElicitationInvalidOrUnsupportedFormsDecline(t *testing.T) {
	for _, tc := range []struct{ name, property, answer string }{
		{"invalid boolean", `{"type":"boolean"}`, "yes"},
		{"multiple values", `{"type":"boolean"}`, "true false"},
		{"fractional integer", `{"type":"integer"}`, "1.5"},
		{"invalid number", `{"type":"number"}`, "NaN"},
		{"invalid enum", `{"type":"string","enum":["unit"]}`, "other"},
		{"missing required", `{"type":"string"}`, ""},
		{"nested object", `{"type":"object","properties":{"child":{"type":"string"}}}`, "{}"},
		{"array", `{"type":"array","items":{"type":"string"}}`, "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, params := elicitationRequest(t, `{"mode":"form","requestedSchema":{"type":"object","properties":{"field":`+tc.property+`},"required":["field"]}}`)
			reason := ""
			s := &providerSession{hooks: Hooks{Questions: func(context.Context, []model.Question) (map[string]string, error) {
				return map[string]string{"field": tc.answer}, nil
			}, Event: func(kind string, value any) {
				if kind == "elicitation_declined" {
					reason, _ = value.(map[string]string)["reason"]
				}
			}}}
			response, err := s.mcpElicitation(context.Background(), m, params)
			value, _ := response.(map[string]any)
			if err != nil || value["action"] != "decline" || value["content"] != nil || reason == "" {
				t.Fatalf("invalid form accepted or unclear: %#v %v", response, err)
			}
		})
	}
}

func TestMCPElicitationSecretFormsDeclineWithoutPersistingSecrets(t *testing.T) {
	m, params := elicitationRequest(t, `{"mode":"form","requestedSchema":{"type":"object","properties":{"token":{"type":"string","format":"password"}},"required":["token"]}}`)
	s := &providerSession{hooks: Hooks{Questions: func(context.Context, []model.Question) (map[string]string, error) {
		t.Fatal("credential requested through persisted question UI")
		return map[string]string{"token": "private-token"}, nil
	}}}
	response, err := s.mcpElicitation(context.Background(), m, params)
	encoded, _ := json.Marshal(response)
	if err != nil || !strings.Contains(string(encoded), `"action":"decline"`) || strings.Contains(string(encoded), "private-token") {
		t.Fatalf("secret form: %s %v", encoded, err)
	}
}

func TestMCPElicitationCancellationReturnsCancel(t *testing.T) {
	m, params := elicitationRequest(t, `{"mode":"form","requestedSchema":{"type":"object","properties":{"job":{"type":"string"}},"required":["job"]}}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &providerSession{hooks: Hooks{Questions: func(context.Context, []model.Question) (map[string]string, error) { return nil, context.Canceled }}}
	response, err := s.mcpElicitation(ctx, m, params)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, map[string]any{"action": "cancel"}) {
		t.Fatalf("cancelled response: %#v %v", response, err)
	}
}

func TestNativeReadToolApprovalUsesCatalogAuthorization(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		s := &providerSession{input: Input{NativeIntegrations: true, ReadOnly: true}, nativeCalls: map[string]nativeCall{"ci-call": {Ref: "app://jenkins", Tool: "build_status", ReadOnly: readOnly}}, hooks: Hooks{Permission: func(context.Context, Permission) (bool, error) {
			if readOnly {
				t.Fatal("catalog-authorized native read requested host mutation permission")
			}
			return false, nil
		}}}
		raw := json.RawMessage(`{"itemId":"ci-call"}`)
		response, err := s.requestPermission(context.Background(), Message{Method: "item/tool/requestApproval", Params: raw}, map[string]any{"itemId": "ci-call"})
		want := "decline"
		if readOnly {
			want = "accept"
		}
		if err != nil || !reflect.DeepEqual(response, map[string]any{"decision": want}) {
			t.Fatalf("native tool approval: %#v %v", response, err)
		}
	}
}

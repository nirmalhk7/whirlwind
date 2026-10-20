package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"whirlwind/internal/model"
)

func verifyProtocolMessages(t *testing.T, fixture protocolFixture) {
	t.Helper()
	input := Input{CWD: t.TempDir(), Prompt: contractPrompt, ReadOnly: fixture.ReadOnly}
	if fixture.Resume {
		input.SessionID = testSessionID
	}
	var mu sync.Mutex
	var events []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := contractAdapter(t, fixture).Run(ctx, input, Hooks{Event: func(kind string, _ any) {
		mu.Lock()
		events = append(events, kind)
		mu.Unlock()
	}})
	if err != nil || out.SessionID != testSessionID || out.Text != contractReply {
		t.Fatalf("message round trip: %+v, %v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 {
		t.Fatalf("missing session/message events: %v", events)
	}
}

func TestACPResumeExcludesReplayedHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: "replay", Resume: true}).Run(ctx,
		Input{CWD: t.TempDir(), SessionID: testSessionID, Prompt: contractPrompt}, Hooks{})
	if err != nil || out.Text != contractReply {
		t.Fatalf("current response includes history: %+v, %v", out, err)
	}
}

func TestACPResumeCapabilityAndEmptyLoadResult(t *testing.T) {
	for _, scenario := range []string{contractNoLoad, contractEmptyLoad} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := contractAdapter(t, protocolFixture{Kind: "acp", Scenario: scenario, Resume: true}).Run(ctx,
				Input{CWD: t.TempDir(), SessionID: testSessionID, Prompt: contractPrompt}, Hooks{})
			if scenario == contractNoLoad {
				if ClassifyFailure(err) != FailureConfiguration || out.Text != "" {
					t.Fatalf("unsupported resume silently replaced session: %+v %v", out, err)
				}
				return
			}
			if err != nil || out.SessionID != testSessionID || out.Text != contractReply {
				t.Fatalf("resume capability/load response mishandled: %+v, %v", out, err)
			}
		})
	}
}

func TestProtocolFailures(t *testing.T) {
	cases := []struct{ kind, scenario, want string }{
		{"acp", contractInitError, contractAuthError},
		{"codex", contractInitError, contractAuthError},
		{"acp", "incompatible-version", "protocol version"},
		{"acp", contractEmptySession, "ACP returned empty session ID"},
		{"codex", contractEmptySession, "codex returned empty thread ID"},
		{"acp", "refusal", "refusal"},
		{"codex", "failed", "fixture turn failed"},
		{"codex", "interrupted", "interrupted"},
		{"acp", "malformed", "malformed provider frame"},
		{"codex", "malformed", "malformed provider frame"},
		{"acp", "exit", "exited"},
		{"codex", "exit", "exited"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := contractAdapter(t, protocolFixture{Kind: tc.kind, Scenario: tc.scenario}).Run(ctx,
				Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestProtocolCancellationDuringPrompt(t *testing.T) {
	for _, kind := range []string{"acp", "codex"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := contractAdapter(t, protocolFixture{Kind: kind, Scenario: "stall"}).Run(ctx,
				Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{Event: func(kind string, _ any) {
					if kind == "fixture/prompt" {
						cancel()
					}
				}})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestProtocolCallbacks(t *testing.T) {
	for _, kind := range []string{"acp", "codex"} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/allow=%t", kind, allow), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				verifyProtocolCallbacks(t, ctx, kind, allow)
			})
		}
	}
}

func verifyProtocolCallbacks(t *testing.T, ctx context.Context, kind string, allow bool) {
	t.Helper()
	scenario := "deny"
	if allow {
		scenario = "allow"
	}
	out, err := contractAdapter(t, protocolFixture{Kind: kind, Scenario: scenario}).Run(ctx,
		Input{CWD: t.TempDir(), Prompt: contractPrompt}, Hooks{
			Permission: func(_ context.Context, p Permission) (bool, error) {
				if !reflect.DeepEqual(p.Command, []string{"go", "test", "./..."}) {
					return false, fmt.Errorf("command lost: %v", p.Command)
				}
				return allow, nil
			},
			Questions: func(_ context.Context, questions []model.Question) (map[string]string, error) {
				if len(questions) != 1 || questions[0].ID != "db" || questions[0].Text != "Which database?" || !reflect.DeepEqual(questions[0].Options, []string{"SQLite", "PostgreSQL"}) {
					return nil, fmt.Errorf("question lost: %+v", questions)
				}
				return map[string]string{"db": "SQLite"}, nil
			},
		})
	if err != nil || out.Text != contractReply {
		t.Fatalf("callback round trip: %+v, %v", out, err)
	}
}

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync/atomic"
	"testing"
)

type actionTestWriteCloser struct{ *bytes.Buffer }

func (actionTestWriteCloser) Close() error { return nil }

func TestRPCPersistsActionBoundariesAroundSideEffectingHandler(t *testing.T) {
	var events []string
	var actionResult map[string]any
	rpc := &RPC{
		input: &actionTestWriteCloser{Buffer: &bytes.Buffer{}},
		handler: func(context.Context, Message) (any, error) {
			return map[string]any{"terminalId": "term-1"}, nil
		},
		observe: func(kind string, payload any) {
			events = append(events, kind)
			if kind == "whirlwind/action_result" {
				encoded, _ := json.Marshal(payload)
				_ = json.Unmarshal(encoded, &actionResult)
			}
		},
	}
	rpc.handle(Message{ID: json.RawMessage(`7`), Method: "terminal/create"})
	if !reflect.DeepEqual(events, []string{"whirlwind/action_started", "whirlwind/tool_result", "whirlwind/action_result"}) {
		t.Fatalf("side-effecting request boundaries were not observed in order: %v", events)
	}
	if actionResult["action_id"] != "7" || actionResult["method"] != "terminal/create" || actionResult["status"] != "completed" || actionResult["terminal_id"] != "term-1" {
		t.Fatalf("terminal completion did not retain reconciliation identity: %v", actionResult)
	}
}

func TestRPCDoesNotRunSideEffectWhenActionStartCannotBePersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called atomic.Bool
	rpc := &RPC{
		ctx:   ctx,
		input: &actionTestWriteCloser{Buffer: &bytes.Buffer{}},
		handler: func(context.Context, Message) (any, error) {
			called.Store(true)
			return nil, nil
		},
		observe: func(kind string, _ any) {
			if kind == "whirlwind/action_started" {
				cancel()
			}
		},
	}
	rpc.handle(Message{ID: json.RawMessage(`8`), Method: "fs/write_text_file"})
	if called.Load() {
		t.Fatal("side-effecting handler ran after action-start persistence canceled the attempt")
	}
}

func TestRPCDoesNotAcknowledgeSideEffectWhenActionResultCannotBePersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var effects atomic.Int32
	output := &actionTestWriteCloser{Buffer: &bytes.Buffer{}}
	rpc := &RPC{
		ctx:   ctx,
		input: output,
		handler: func(context.Context, Message) (any, error) {
			effects.Add(1)
			return map[string]any{"written": true}, nil
		},
		observe: func(kind string, _ any) {
			if kind == "whirlwind/action_result" {
				// The workflow event callback cancels the attempt when its durable
				// action-result record cannot be committed.
				cancel()
			}
		},
	}
	request := Message{ID: json.RawMessage(`9`), Method: "fs/write_text_file"}
	rpc.handle(request)
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode action response: %v; raw=%s", err, output.String())
	}
	if effects.Load() != 1 {
		t.Fatalf("side effect count=%d, want exactly one", effects.Load())
	}
	if len(response.Error) == 0 || len(response.Result) != 0 {
		t.Fatalf("action-result persistence failure was acknowledged as success: %s", output.String())
	}
	rpc.handle(request)
	if effects.Load() != 1 {
		t.Fatalf("cancelled attempt replayed the side effect: count=%d", effects.Load())
	}
}

var _ io.WriteCloser = actionTestWriteCloser{}

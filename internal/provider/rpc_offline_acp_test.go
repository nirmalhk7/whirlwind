package provider

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
)

func TestOfflineACPFramesPreserveChannelsAndRejectTruncation(t *testing.T) {
	frame := func(channel byte, payload string) []byte {
		var header [8]byte
		header[0] = channel
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		return append(header[:], payload...)
	}
	input := append(frame(2, "diagnostics"), frame(1, "rpc output")...)
	var stdout, stderr bytes.Buffer
	if err := copyOfflineACPFrames(bytes.NewReader(input), &stdout, &stderr); err != nil || stdout.String() != "rpc output" || stderr.String() != "diagnostics" {
		t.Fatalf("bad container stream: %q %q %v", stdout.String(), stderr.String(), err)
	}
	for _, invalid := range [][]byte{input[:len(input)-1], frame(0, "unrecognized"), {1, 0, 0, 0, 1, 0, 0, 0}} {
		if err := copyOfflineACPFrames(bytes.NewReader(invalid), &stdout, &stderr); err == nil {
			t.Fatal("accepted invalid container stream")
		}
	}
}

func TestOfflineACPNotificationsAllowOnlyAgentText(t *testing.T) {
	calls := 0
	callback := offlineACPNotifications(func(context.Context, Message) (any, error) {
		calls++
		return nil, nil
	})
	ctx := context.Background()
	if _, err := callback(ctx, Message{Method: "terminal/create", ID: []byte("1")}); err == nil || calls != 0 {
		t.Fatal("an offline fixture reached a host callback")
	}
	_, _ = callback(ctx, Message{Method: "whirlwind/run_check"})
	_, _ = callback(ctx, Message{Method: "session/update", Params: []byte(`{"update":{"sessionUpdate":"tool_call"}}`)})
	if calls != 0 {
		t.Fatal("non-text fixture notification reached the host handler")
	}
	_, _ = callback(ctx, Message{Method: "session/update", Params: []byte(`{"update":{"sessionUpdate":"agent_message_chunk"}}`)})
	if calls != 1 {
		t.Fatal("fixture text did not reach the host handler")
	}
}

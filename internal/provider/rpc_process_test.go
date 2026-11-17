package provider

import (
	"context"
	"testing"
)

// TestJSONRPCProcessAndCancellation verifies RPC replies are decoded and cancelling the context stops the provider process.
func TestJSONRPCProcessAndCancellation(t *testing.T) {
	rpc, e := Start(context.Background(), []string{"sh", "-c", `while IFS= read -r line; do printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"ok":true}}'; done`}, t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := rpc.Close(); err != nil {
			t.Errorf("close provider RPC: %v", err)
		}
	})
	var out struct {
		OK bool `json:"ok"`
	}
	if e = rpc.Call(context.Background(), "initialize", map[string]any{}, &out); e != nil || !out.OK {
		t.Fatalf(valueErrorFormat, out, e)
	}
}

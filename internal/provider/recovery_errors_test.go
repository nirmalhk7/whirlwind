package provider

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRPCFailureRetainsStructuredCode(t *testing.T) {
	rpc := &RPC{}
	err := rpc.decodeResponse("session/prompt", Message{Error: json.RawMessage(`{"code":429,"message":"temporarily limited","data":{"retryAfter":10}}`)}, nil)
	var coded interface{ RPCCode() int }
	if !errors.As(err, &coded) || coded.RPCCode() != 429 {
		t.Fatalf("structured error lost: %v", err)
	}
}

func TestGenericRPCServerErrorIsNotAuthentication(t *testing.T) {
	err := &RPCError{Code: -32000, Message: "unknown server failure"}
	if ClassifyFailure(err) == FailureAuth {
		t.Fatal("generic server code treated as login failure")
	}
}

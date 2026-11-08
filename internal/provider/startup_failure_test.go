package provider

import (
	"context"
	"strings"
	"testing"
	"time"
)

const startupTestShell = "/bin/sh"

func TestStartupExitRetainsSetupDiagnosis(t *testing.T) {
	for _, code := range []string{"126", "127"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		rpc, err := Start(ctx, []string{startupTestShell, "-c", "echo 'env: node: No such file or directory' >&2; exit " + code}, t.TempDir(), nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		<-rpc.done
		err = rpc.closedError()
		closeStartupRPC(t, rpc)
		cancel()
		if ClassifyFailure(err) != FailureConfiguration || !strings.Contains(err.Error(), "node: No such file") || !strings.Contains(err.Error(), code) {
			t.Fatalf("startup diagnosis lost: %v", err)
		}
	}
}

func TestExitAfterProtocolResponseRemainsTransient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rpc, err := Start(ctx, []string{startupTestShell, "-c", `read request; echo '{"id":1,"result":{}}'; sleep 0.05; exit 127`}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = rpc.Call(ctx, "initialize", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	<-rpc.done
	defer closeStartupRPC(t, rpc)
	if err = rpc.closedError(); ClassifyFailure(err) != FailureTransient {
		t.Fatalf("runtime exit became setup failure: %v", err)
	}
}

func TestStartupRegistryPolicyDenialBlocksSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	diagnostic := "npm error 403 host is not on the allow list - GET https://registry.npmjs.org/@agentclientprotocol%2fcodex-acp"
	rpc, err := Start(ctx, []string{startupTestShell, "-c", "printf '%s\\n' '" + diagnostic + "' >&2; exit 1"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-rpc.done
	defer closeStartupRPC(t, rpc)
	err = rpc.closedError()
	if ClassifyFailure(err) != FailureConfiguration || !strings.Contains(err.Error(), "registry.npmjs.org") {
		t.Fatalf("policy setup failure lost: %v", err)
	}
}

func closeStartupRPC(t *testing.T, rpc *RPC) {
	t.Helper()
	if err := rpc.Close(); err != nil {
		t.Errorf("provider process cleanup: %v", err)
	}
}

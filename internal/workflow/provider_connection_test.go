package workflow

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

type connectionAdapterFunc func(context.Context, provider.Input, provider.Hooks) (provider.Output, error)

const connectionGreeting = "Hello World"

func (f connectionAdapterFunc) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	return f(ctx, in, hooks)
}

func TestProviderConnectionUsesTemporaryReadOnlyWorkspace(t *testing.T) {
	e, _, _ := testEngine(t)
	var workspace string
	e.Adapter = func(p model.Provider) provider.Adapter {
		if p.ID != "chosen" {
			t.Errorf("tested wrong provider: %s", p.ID)
		}
		return connectionAdapterFunc(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
			workspace = in.CWD
			assertConnectionInput(t, ctx, in, hooks)
			return provider.Output{Text: connectionGreeting, SessionID: "connection-one"}, nil
		})
	}
	out, err := e.TestProviderConnection(context.Background(), model.Provider{ID: "chosen", Kind: "acp"})
	if err != nil || out.Text != connectionGreeting || out.SessionID != "connection-one" {
		t.Fatalf("reply lost: %+v, %v", out, err)
	}
	if workspace == "" {
		t.Fatal("agent was not called")
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("temporary workspace not cleaned up: %v", err)
	}
}

func assertConnectionInput(t *testing.T, ctx context.Context, in provider.Input, hooks provider.Hooks) {
	t.Helper()
	if !in.ReadOnly || in.SessionID != "" || !strings.Contains(in.Prompt, connectionGreeting) || hooks.Permission != nil {
		t.Errorf("unsafe connectivity input: %+v", in)
	}
	if _, err := os.Stat(in.CWD); err != nil {
		t.Errorf("workspace missing during run: %v", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Error("connection test has no deadline")
	}
}

func TestProviderConnectionReportsFailureAndEmptyReply(t *testing.T) {
	for _, failure := range []error{errors.New("authentication required"), nil} {
		e, _, _ := testEngine(t)
		e.Adapter = func(model.Provider) provider.Adapter {
			return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
				return provider.Output{}, failure
			})
		}
		_, err := e.TestProviderConnection(context.Background(), model.Provider{ID: "chosen", Kind: "codex"})
		if err == nil || (failure != nil && !errors.Is(err, failure)) {
			t.Fatalf("failure swallowed: %v", err)
		}
	}
}

func TestProviderConnectionHonorsCapacityCancellation(t *testing.T) {
	e, _, _ := testEngine(t)
	e.active["occupied"] = 4
	e.Adapter = func(model.Provider) provider.Adapter {
		t.Error("agent started despite occupied capacity")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := e.TestProviderConnection(ctx, model.Provider{ID: "chosen", Kind: "acp"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait ignored cancellation: %v", err)
	}
}

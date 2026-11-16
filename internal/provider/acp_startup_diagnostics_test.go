package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
)

const metadataExitArgument = "whirlwind-metadata-exit"

func TestACPMetadataEarlyExitHasSelectedProfileGuidance(t *testing.T) {
	for _, tc := range []struct{ profile, guidance, credential string }{
		{"codex", "codex-acp", "CODEX_API_KEY"},
		{"claude", "claude-agent-acp", "CLAUDE_CODE_OAUTH_TOKEN"},
		{"cursor", "agent login", "CURSOR_API_KEY"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			installProtocolOpenShell(t)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			p := model.Provider{ID: "metadata-" + tc.profile, Kind: "acp", ACPProfile: tc.profile, Command: []string{executable, "-test.run=^TestACPMetadataEarlyExitHelper$", "--", metadataExitArgument}}
			for _, operation := range []string{"catalog", "connection"} {
				t.Run(operation, func(t *testing.T) {
					assertMetadataFailureGuidance(t, p, operation, []string{tc.guidance, tc.credential})
				})
			}
		})
	}
}

func assertMetadataFailureGuidance(t *testing.T, p model.Provider, operation string, hints []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := metadataProbeFailure(ctx, p, t.TempDir(), t.TempDir(), operation)
	var transport *TransportError
	if err == nil || !errors.As(err, &transport) || ClassifyFailure(err) != FailureTransient {
		t.Fatalf("early exit lost transport failure semantics: %v", err)
	}
	for _, hint := range append([]string{p.ID}, hints...) {
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("early exit lacks selected profile guidance %q: %v", hint, err)
		}
	}
}

func metadataProbeFailure(ctx context.Context, p model.Provider, cwd, registry, operation string) error {
	if operation == "catalog" {
		_, err := ModelsWithSupervisorDir(ctx, p, cwd, registry)
		return err
	}
	_, err := CheckConnectionWithSupervisorDir(ctx, p, cwd, registry)
	return err
}

func TestACPMetadataGuidanceCannotChangeFailureClassification(t *testing.T) {
	installProtocolOpenShell(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"unauthorized", "unsupported"} {
		t.Run(alias, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p := model.Provider{ID: alias, Kind: "acp", ACPProfile: "codex", Command: []string{executable, "-test.run=^TestACPMetadataEarlyExitHelper$", "--", metadataExitArgument}}
			failure := metadataProbeFailure(ctx, p, t.TempDir(), t.TempDir(), "catalog")
			if failure == nil || ClassifyFailure(failure) != FailureTransient {
				t.Fatalf("provider alias changed a transient early exit classification: %v", failure)
			}
			combined := errors.Join(failure, errors.New("tool permission denied by sandbox"))
			if ClassifyFailure(combined) != FailurePolicy {
				t.Fatalf("metadata guidance hid a joined policy failure: %v", combined)
			}
		})
	}
}

func TestACPMetadataEarlyExitHelper(t *testing.T) {
	if !slices.Contains(os.Args, metadataExitArgument) {
		return
	}
	var request Message
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil || request.Method != "initialize" {
		os.Exit(2)
	}
	_, _ = fmt.Fprintln(os.Stderr, "fixture stopped before ACP initialization")
	os.Exit(1)
}

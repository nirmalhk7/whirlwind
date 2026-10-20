package workflow

import (
	"errors"
	"testing"
	"whirlwind/internal/provider"
)

func TestProviderFailureRecognizesDisabledClaudeSubscription(t *testing.T) {
	failure := errors.New("provider RPC: Claude Code returned an error result: Prompt is too long · automatic compaction failed: Your organization has disabled Claude subscription access for Claude Code · Use an Anthropic API key instead, or ask your admin to enable access")
	if kind := provider.ClassifyFailure(failure); kind != provider.FailureAuth {
		t.Fatalf("disabled subscription must trigger authentication fallback: kind=%s", kind)
	}
}

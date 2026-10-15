package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestWebhookSignatureRejectsTampering verifies webhook authentication rejects altered payloads or signatures.
func TestWebhookSignatureRejectsTampering(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !ValidSignature("secret", sig, body) || ValidSignature("secret", sig, []byte("changed")) || ValidSignature("", sig, body) {
		t.Fatal("signature validation")
	}
}

// TestIssueCreationReconcilesUncertainRemoteWrite verifies retrying issue publication finds the previously created plan marker.
func TestIssueCreationReconcilesUncertainRemoteWrite(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "remote.json")
	script := `#!/bin/sh
case "$1 $2" in
"issue list") if [ -f "$WHIRLWIND_REMOTE_STATE" ]; then cat "$WHIRLWIND_REMOTE_STATE"; else printf '[]'; fi ;;
"label create") exit 0 ;;
"issue create") printf '[{"number":7,"title":"Feature","body":"body\\n\\n<!-- whirlwind-plan:test -->","state":"OPEN","labels":[{"name":"whirlwind:plan"}],"updatedAt":"1","url":"https://github.com/o/r/issues/7"}]' > "$WHIRLWIND_REMOTE_STATE"; echo 'connection lost after remote write' >&2; exit 1 ;;
*) exit 2 ;;
esac
`
	if e := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("WHIRLWIND_REMOTE_STATE", state)
	c := CLI{}
	ctx := context.Background()
	if _, e := c.CreateIssue(ctx, "o/r", "Feature", "body", "<!-- whirlwind-plan:test -->"); e == nil {
		t.Fatal("uncertain write must report failure")
	}
	issue, e := c.CreateIssue(ctx, "o/r", "Feature", "body", "<!-- whirlwind-plan:test -->")
	if e != nil || issue.Number != 7 {
		t.Fatalf("reconciliation %+v %v", issue, e)
	}
}

package redaction

import (
	"strings"
	"testing"
	"time"
)

func TestStartedSecretRegistrationDoesNotExpireBeforeRelease(t *testing.T) {
	const secret = "long-running-sandbox-secret-0123456789"
	release, err := RegisterSecret(secret, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := KeepSecretUntilReleased(secret); err != nil {
		release()
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if got := RedactText("diagnostic=" + secret); strings.Contains(got, secret) {
		t.Fatalf("active process secret expired before process cleanup: %q", got)
	}
	release()
	if got := RedactText(secret); got != secret {
		t.Fatalf("released process secret stayed registered: %q", got)
	}
}

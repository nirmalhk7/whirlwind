package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParityFreshStateDoesNotReuseLegacyApplicationRoot(t *testing.T) {
	t.Setenv("WHIRLWIND_DATA_DIR", "")
	if got := filepath.Base(defaultData()); got != "parity-v1" {
		t.Fatalf("new execution would reuse legacy state: %s", defaultData())
	}
	if !strings.Contains(defaultData(), "whirlwind") {
		t.Fatal("fresh state must stay beneath Whirlwind application data")
	}
}

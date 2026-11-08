package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/process"
	"whirlwind/internal/redaction"
	"whirlwind/internal/store"
)

func TestRPCCleanupFailureIsReportedBeforeProcessExit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", root)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var exit map[string]any
	rpc, err := Start(ctx, []string{"sh", "-c", "cat"}, t.TempDir(), nil, func(kind string, value any) {
		if kind == "whirlwind/process_exit" {
			mu.Lock()
			exit = value.(map[string]any)
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Close(); err == nil {
		t.Fatal("close hid process cleanup failure")
	}
	mu.Lock()
	defer mu.Unlock()
	if exit["cleanup_verified"] != false || exit["cleanup_error"] == nil {
		t.Fatalf("provider exit claimed no cleanup failure: %+v", exit)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 {
		t.Fatalf("failed cleanup lost recovery evidence: %v %v", files, err)
	}
}

func TestRPCPipeSetupFailureReleasesOpenShellRedactionState(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "openshell"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile, err := execution.ForPolicy(t.TempDir(), true, "policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := execution.Command(ctx, []string{"image-only-agent"}, profile)
	if err != nil {
		t.Fatal(err)
	}
	var token string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "WHIRLWIND_OPENSHELL_PROXY_TOKEN=") {
			token = strings.TrimPrefix(entry, "WHIRLWIND_OPENSHELL_PROXY_TOKEN=")
		}
	}
	if token == "" || store.RedactText(token) != "[redacted]" {
		t.Fatal("OpenShell capability was not registered before process launch")
	}
	cmd.Stdin = strings.NewReader("already assigned")
	_, launchCancel := context.WithCancel(ctx)
	_, err = startCommand(ctx, launchCancel, cmd, nil)
	launchCancel()
	if err == nil || cmd.Process != nil {
		t.Fatalf("pipe setup unexpectedly launched a process: err=%v process=%v", err, cmd.Process)
	}
	if got := store.RedactText(token); got != token {
		t.Fatalf("failed pre-start setup retained an unusable secret registration: %q", got)
	}
}

func TestRPCDrainsTrailingStderrBeforeReleasingCredentialRedaction(t *testing.T) {
	var capability [32]byte
	if _, err := rand.Read(capability[:]); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(capability[:])
	registry := t.TempDir()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := process.Command(ctx, "sh", "-c", "printf '%s' \"$WW_TEST_SECRET\" >&2")
	cmd.Env = append(cmd.Env, "WW_TEST_SECRET="+secret)
	var processToken string
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=") {
			processToken = strings.TrimPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=")
		}
	}
	releaseSecret, err := redaction.RegisterSecret(secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	if err := process.OnCleanup(cmd, func() { releaseSecret(); close(released) }); err != nil {
		releaseSecret()
		t.Fatal(err)
	}
	observed := make(chan struct{})
	runtimeCleaned := make(chan struct{})
	continueCapture := make(chan struct{})
	captured := make(chan string, 1)
	rpc, err := startCommandWithCleanup(ctx, cancel, cmd, nil, func() error {
		close(runtimeCleaned)
		return nil
	}, func(kind string, value any) {
		if kind != "whirlwind/stderr" {
			return
		}
		payload := value.(map[string]any)["text"].(string)
		close(observed)
		<-continueCapture
		captured <- store.RedactText(payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-observed:
	case <-time.After(3 * time.Second):
		t.Fatal("provider stderr did not reach its capture boundary")
	}
	select {
	case <-runtimeCleaned:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime cleanup did not finish while stderr capture was held")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("provider process remained alive after runtime cleanup: %v", err)
	}
	registration := filepath.Join(registry, processToken+".json")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, statErr := os.Stat(registration); os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process group cleanup did not finish while stderr capture was held")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-released:
		t.Fatal("credential was released before trailing stderr reader drained")
	case <-time.After(50 * time.Millisecond):
	}
	if got := store.RedactText(secret); got != "[redacted]" {
		t.Fatalf("credential redaction ended before stderr capture drained: %q", got)
	}
	close(continueCapture)
	if err := rpc.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("credential cleanup callback did not run after stderr drain")
	}
	if got := <-captured; strings.Contains(got, secret) {
		t.Fatalf("trailing stderr persisted the raw credential: %q", got)
	}
	if got := store.RedactText(secret); got != secret {
		t.Fatalf("credential registration remained after successful output drain: %q", got)
	}
}

func TestRPCCleanupFailureRetainsCredentialRedaction(t *testing.T) {
	var capability [32]byte
	if _, err := rand.Read(capability[:]); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(capability[:])
	releaseSecret, err := redaction.RegisterSecret(secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecret()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := process.Command(ctx, "sh", "-c", "exit 0")
	if err := process.OnCleanup(cmd, releaseSecret); err != nil {
		t.Fatal(err)
	}
	rpc, err := startCommandWithCleanup(ctx, cancel, cmd, nil, func() error {
		return errors.New("controlled runtime cleanup failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rpc.Close(); err == nil || !strings.Contains(err.Error(), "controlled runtime cleanup failure") {
		t.Fatalf("runtime cleanup failure was not reported: %v", err)
	}
	if got := store.RedactText(secret); got != "[redacted]" {
		t.Fatalf("runtime cleanup failure released credential redaction: %q", got)
	}
}

package process

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	cleanupFixtureRunFlag = "-test.run=^TestCleanupFixture$"
	cleanupSpawnWait      = "spawn-wait"
)

const cleanupFixtureMode = "WHIRLWIND_CLEANUP_FIXTURE="

type cleanupFixture struct {
	PID     int    `json:"pid"`
	Address string `json:"address"`
}

// Helpers use a readiness pipe, not sleeps, so the assertions always run after
// the detached descendant has started serving its own socket.
func TestCleanupFixture(t *testing.T) {
	mode := os.Getenv("WHIRLWIND_CLEANUP_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "listener" {
		serveCleanupFixture()
	}
	spawnCleanupFixture(mode)
}

func serveCleanupFixture() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(cleanupFixture{os.Getpid(), listener.Addr().String()}); err != nil {
		panic(err)
	}
	_ = os.Stdout.Close()
	for {
		connection, err := listener.Accept()
		if err != nil {
			os.Exit(2)
		}
		_ = connection.Close()
	}
}

func spawnCleanupFixture(mode string) {
	child := exec.Command(os.Args[0], cleanupFixtureRunFlag)
	child.Env = append(cleanupFixtureEnvironment(), cleanupFixtureMode+"listener")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := child.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err = child.Start(); err != nil {
		panic(err)
	}
	var fixture cleanupFixture
	if err = json.NewDecoder(stdout).Decode(&fixture); err != nil {
		panic(err)
	}
	_ = stdout.Close()
	if err = json.NewEncoder(os.Stdout).Encode(fixture); err != nil {
		panic(err)
	}
	_ = os.Stdout.Close()
	if mode == cleanupSpawnWait {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
		_, _ = listener.Accept()
	}
	os.Exit(0)
}

func cleanupFixtureEnvironment() []string {
	var environment []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, cleanupFixtureMode) {
			environment = append(environment, entry)
		}
	}
	return environment
}

func TestRunWaitsForDetachedDescendantCleanup(t *testing.T) {
	for _, mode := range []string{"spawn-exit", cleanupSpawnWait} {
		t.Run(mode, func(t *testing.T) { assertDetachedCleanup(t, mode) })
	}
}

func assertDetachedCleanup(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", t.TempDir())
	// Cleanup must use OS ownership, independently of executable lookup.
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close fixture reader: %v", err)
		}
	}()
	defer func() {
		if err := writer.Close(); err != nil {
			t.Errorf("close fixture writer: %v", err)
		}
	}()
	cmd := Command(ctx, os.Args[0], cleanupFixtureRunFlag)
	cmd.Env = append(cmd.Env, cleanupFixtureMode+mode)
	cmd.Stdout = writer
	addressReady := make(chan struct{})
	cleanupObservedStoppedListener := make(chan bool, 1)
	fixtureAddress := ""
	if err := OnCleanup(cmd, func() {
		<-addressReady
		cleanupObservedStoppedListener <- !fixtureListenerAccepting(fixtureAddress)
	}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- Run(cmd) }()
	fixture := readCleanupFixture(t, reader)
	fixtureAddress = fixture.Address
	close(addressReady)
	t.Cleanup(func() { _ = syscall.Kill(fixture.PID, syscall.SIGKILL) })
	if mode == cleanupSpawnWait {
		cancel()
	}
	waitForCleanup(t, mode, finished)
	assertFixtureListenerStopped(t, fixture.Address)
	select {
	case stopped := <-cleanupObservedStoppedListener:
		if !stopped {
			t.Fatal("cleanup callback ran before the detached process listener stopped")
		}
	case <-time.After(time.Second):
		t.Fatal("process cleanup callback did not run after process-group drain")
	}
}

func fixtureListenerAccepting(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func readCleanupFixture(t *testing.T, reader *os.File) cleanupFixture {
	t.Helper()
	var fixture cleanupFixture
	if err := json.NewDecoder(reader).Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func waitForCleanup(t *testing.T, mode string, finished <-chan error) {
	t.Helper()
	select {
	case err := <-finished:
		if mode == "spawn-exit" && err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("managed process cleanup did not finish")
	}
}

func assertFixtureListenerStopped(t *testing.T, address string) {
	t.Helper()
	if connection, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		_ = connection.Close()
		t.Fatal("Run returned while detached agent descendant was still running")
	}
}

func TestReapDoesNotTreatCommandArgumentsAsOwnership(t *testing.T) {
	root := t.TempDir()
	owned := Command(context.Background(), "unused")
	unowned := exec.Command(os.Args[0], cleanupFixtureRunFlag, "--", processTokenPrefix+token(owned))
	unowned.Env = append(cleanupFixtureEnvironment(), cleanupFixtureMode+"listener")
	unowned.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := unowned.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := unowned.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unowned.Process.Kill(); _ = unowned.Wait() })
	var fixture cleanupFixture
	if err := json.NewDecoder(stdout).Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	_ = stdout.Close()
	if err := writeRegistration(root, registration{Token: token(owned)}); err != nil {
		t.Fatal(err)
	}
	if err := Reap(root); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", fixture.Address, time.Second)
	if err != nil {
		t.Fatalf("unrelated process killed because its arguments mentioned a token: %v", err)
	}
	_ = connection.Close()
	if entries, err := filepath.Glob(filepath.Join(root, "*.json")); err != nil || len(entries) != 0 {
		t.Fatal(fmt.Sprint(entries, err))
	}
}

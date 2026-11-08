package provider

import (
	"context"
	"io"
	"testing"
	"time"

	"whirlwind/internal/process"
)

func TestRPCPipesPreserveStderrWhenDiagnosticReaderRunsAfterProcessExit(t *testing.T) {
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := process.Command(ctx, "sh", "-c", "printf '%s' 'trailing diagnostic' >&2")
	pipes, err := rpcPipes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer pipes.input.Close()
	defer pipes.output.Close()
	defer pipes.stderr.Close()
	for _, writer := range []io.Writer{cmd.Stdout, cmd.Stderr} {
		if closer, ok := writer.(io.Closer); ok {
			defer closer.Close()
		}
	}
	if err := process.Start(cmd); err != nil {
		t.Fatal(err)
	}
	// A busy supervisor can reap the child before scheduling its diagnostic
	// reader. Bytes already written must remain available to that reader.
	release, err := process.WaitForDrain(cmd)
	if release != nil {
		defer release()
	}
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("trailing diagnostic"))
	if _, err := io.ReadFull(pipes.stderr, got); err != nil || string(got) != "trailing diagnostic" {
		t.Fatalf("process exit discarded pending stderr: %q %v", got, err)
	}
}

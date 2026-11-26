// whirlwind-verify executes one frozen local job. Workflow authority stays in
// the supervisor; this executable receives only a bounded manifest on stdin.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"whirlwind/internal/verification"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("verification manifest exceeds 1 MiB")
	}
	manifest, err := decodeManifest(raw, os.Args[1:])
	if err != nil {
		return err
	}
	if err := os.Setenv("WHIRLWIND_PROCESS_REGISTRY", filepath.Join(manifest.Root, "processes")); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	encoder := json.NewEncoder(os.Stdout)
	report, err := verification.RunJob(ctx, manifest, func(event verification.JobEvent) error { return encoder.Encode(event) })
	if err != nil {
		return err
	}
	return encoder.Encode(map[string]any{"type": "complete", "manifest_hash": report.ManifestHash, "passed": report.Passed})
}

func decodeManifest(raw []byte, args []string) (verification.JobManifest, error) {
	switch {
	case len(args) == 0:
		var manifest verification.JobManifest
		if err := decodeRequest(raw, &manifest); err != nil {
			return manifest, err
		}
		if manifest.Purpose == verification.LinuxQltyProbePurpose {
			return manifest, errors.New("use --linux-qlty-probe for standalone scanner qualification")
		}
		return manifest, nil
	case len(args) == 1 && args[0] == "--linux-qlty-probe":
		var request verification.LinuxQltyProbeRequest
		if err := decodeRequest(raw, &request); err != nil {
			return verification.JobManifest{}, err
		}
		return request.Manifest(), nil
	default:
		return verification.JobManifest{}, errors.New("usage: whirlwind-verify [--linux-qlty-probe]")
	}
}

func decodeRequest(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid verification manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("verification requires exactly one manifest")
	}
	return nil
}

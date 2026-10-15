package main

import (
	"flag"
	"os"
	"testing"
)

func TestDefaultControlListener(t *testing.T) {
	previousFlags, previousArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = previousFlags, previousArgs })
	flag.CommandLine = flag.NewFlagSet("whirlwind", flag.ContinueOnError)
	os.Args = []string{"whirlwind", "--data", t.TempDir()}
	c, err := readConfig()
	if err != nil {
		t.Fatal(err)
	}
	servers, err := c.servers(nil)
	if err != nil {
		t.Fatal(err)
	}
	if servers[0].Addr != "127.0.0.1:9877" {
		t.Fatalf("control listener = %q, want 127.0.0.1:9877", servers[0].Addr)
	}
}

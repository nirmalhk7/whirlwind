package main

import (
	"flag"
	"os"
	"testing"
)

func TestDefaultRunModeIsDurableLocalSupervisor(t *testing.T) {
	previousFlags, previousArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = previousFlags, previousArgs })
	flag.CommandLine = flag.NewFlagSet("whirlwind", flag.ContinueOnError)
	os.Args = []string{"whirlwind", "--data", t.TempDir()}
	c, err := readConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !c.supervisor {
		t.Fatal("default process mode must be the durable local supervisor, not an HTTP UI server")
	}
}

package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const protocolCursorCredentialPath = ".cursor/cli-config.json"

type protocolAuthExpectation struct {
	Home     string
	Path     string
	Contents string
	Absent   []protocolAuthPath
}

type protocolAuthPath struct {
	Home string
	Path string
}

func catalogAuthExpectation(family string) *protocolAuthExpectation {
	paths := map[string]protocolAuthPath{
		"codex":  {Home: "CODEX_HOME", Path: "auth.json"},
		"claude": {Home: "CLAUDE_CONFIG_DIR", Path: ".credentials.json"},
	}
	want := paths[family]
	var absent []protocolAuthPath
	for other, path := range paths {
		if other != family {
			absent = append(absent, path)
		}
	}
	switch family {
	case "codex", "claude":
		absent = append(absent, protocolAuthPath{Home: "HOME", Path: protocolCursorCredentialPath})
	}
	return &protocolAuthExpectation{Home: want.Home, Path: want.Path, Contents: "synthetic test credential", Absent: absent}
}

func verifyProtocolAuth(t *testing.T, expected *protocolAuthExpectation) {
	t.Helper()
	if expected == nil {
		return
	}
	path := filepath.Join(os.Getenv(expected.Home), filepath.FromSlash(expected.Path))
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != expected.Contents {
		t.Fatalf("selected synthetic %s credential was not copied into the guest home: %v", expected.Home, err)
	}
	for _, missing := range expected.Absent {
		path := filepath.Join(os.Getenv(missing.Home), filepath.FromSlash(missing.Path))
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("unselected synthetic credential was copied into the guest home: %s/%s", missing.Home, missing.Path)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("could not verify unselected synthetic credential absence: %s/%s: %v", missing.Home, missing.Path, err)
		}
	}
}

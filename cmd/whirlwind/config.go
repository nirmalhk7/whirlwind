package main

import (
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type config struct{ data, listen, webhooks, bridge, settings string }

const settingsFileName = "settings.yaml"

func readConfig() (config, error) {
	data := flag.String("data", defaultData(), "private data directory")
	listen := flag.String("listen", "127.0.0.1:9877", "loopback control address")
	webhooks := flag.String("webhook-listen", "", "optional webhook-only listener")
	bridge := flag.String("claude-bridge", "bridges/claude/bridge.mjs", "Claude SDK bridge path")
	settings := flag.String("settings", defaultSettingsFile(), "authoritative settings.yaml path")
	flag.Parse()
	absolute, err := filepath.Abs(*data)
	if err != nil {
		return config{}, err
	}
	*data = absolute
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return config{}, errors.New("control listener must bind loopback")
	}
	settingsPath, err := filepath.Abs(*settings)
	if err != nil {
		return config{}, err
	}
	return config{data: *data, listen: *listen, webhooks: *webhooks, bridge: *bridge, settings: settingsPath}, nil
}
func defaultData() string {
	if value := os.Getenv("WHIRLWIND_DATA_DIR"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".whirlwind-state"
	}
	return filepath.Join(home, ".local", "share", "whirlwind")
}

func defaultSettingsFile() string {
	if value := os.Getenv("WHIRLWIND_SETTINGS_FILE"); value != "" {
		return value
	}
	cwd, err := os.Getwd()
	if err != nil {
		return settingsFileName
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if data, e := os.ReadFile(filepath.Join(dir, "go.mod")); e == nil && strings.HasPrefix(string(data), "module whirlwind") {
			return filepath.Join(dir, settingsFileName)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return filepath.Join(cwd, settingsFileName)
}

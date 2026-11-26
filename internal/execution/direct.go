package execution

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"whirlwind/internal/process"
)

const directLauncherName = "whirlwind-direct-process"

// SandboxEnabled reads the host-only mode. Invalid values always fail closed.
func SandboxEnabled() (bool, error) {
	value, set := os.LookupEnv("WHIRLWIND_SANDBOX")
	if !set || value == "on" {
		return true, nil
	}
	if value == "off" {
		return false, nil
	}
	return false, fmt.Errorf("%w: WHIRLWIND_SANDBOX must be on or off", ErrSandbox)
}

func IsDirectLauncher(launcher string) bool {
	return filepath.Base(launcher) == directLauncherName
}

func directCommand(ctx context.Context, command []string, profile Profile) (*exec.Cmd, error) {
	if len(command) == 0 || !filepath.IsAbs(profile.CWD) || filepath.Clean(profile.CWD) == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: direct execution requires a command and bounded worktree", ErrSandbox)
	}
	if len(profile.Credentials)+len(profile.NativeFiles)+len(profile.ProtectedFiles) != 0 ||
		profile.MCPBroker != nil || profile.MCPBrokerToken != "" || profile.LifecycleHook != nil {
		return nil, fmt.Errorf("%w: direct execution cannot provide sandbox-only grants or protection", ErrSandbox)
	}
	target, err := commandTarget(command[0])
	if err != nil {
		return nil, err
	}
	network, err := directNetworkEnvironment()
	if err != nil {
		return nil, err
	}
	home, cleanup, err := directHome(profile.RuntimeDir)
	if err != nil {
		return nil, err
	}
	cmd := process.Command(ctx, target, command[1:]...)
	cmd.Dir = profile.CWD
	cmd.Env = append(directEnvironment(cmd.Env, profile, home), network...)
	if cleanup != nil {
		if err := process.OnCleanup(cmd, cleanup); err != nil {
			cleanup()
			return nil, err
		}
	}
	return cmd, nil
}

func directHome(runtimeDir string) (string, func(), error) {
	if runtimeDir == "" {
		home, err := os.MkdirTemp("", "whirlwind-direct-home-")
		return home, func() { _ = os.RemoveAll(home) }, err
	}
	home := filepath.Join(runtimeDir, "direct-home")
	if err := os.Mkdir(home, 0700); err != nil && !os.IsExist(err) {
		return "", nil, err
	}
	info, err := os.Lstat(home)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", nil, fmt.Errorf("%w: direct process home must be a private directory", ErrSandbox)
	}
	return home, nil, nil
}

var directNetworkKeys = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR",
	"NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"}

func directNetworkEnvironment() ([]string, error) {
	var env []string
	for _, key := range directNetworkKeys {
		value, set := os.LookupEnv(key)
		if !set {
			continue
		}
		if value != "" && strings.HasSuffix(strings.ToUpper(key), "_PROXY") && !strings.EqualFold(key, "NO_PROXY") {
			address, err := url.Parse(value)
			if err != nil || address.Host == "" || address.User != nil {
				return nil, fmt.Errorf("%w: direct execution requires an outer proxy URL without embedded credentials (%s)", ErrSandbox, key)
			}
		}
		env = append(env, key+"="+value)
	}
	return env, nil
}

func directEnvironment(inherited []string, profile Profile, home string) []string {
	var env []string
	for _, entry := range filteredEnvironment(inherited, profile) {
		key, _, _ := strings.Cut(entry, "=")
		if key == "HOME" || key == "TMPDIR" || key == "WHIRLWIND_OPENSHELL_IMAGE" || key == "WHIRLWIND_PROCESS_REGISTRY" || !DirectEnvironmentAllowed(key) {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home, "TMPDIR="+home, "PWD="+profile.CWD, "CODEX_HOME="+home,
		"CLAUDE_CONFIG_DIR="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0")
}

// DirectEnvironmentAllowed excludes Git and SSH connector state even if named by a profile.
func DirectEnvironmentAllowed(name string) bool {
	if slices.Contains(directNetworkKeys, name) {
		return false
	}
	for _, prefix := range []string{"GIT_", "GITHUB_", "GH_", "SSH_", "GCM_"} {
		if strings.HasPrefix(strings.ToUpper(name), prefix) {
			return false
		}
	}
	return true
}

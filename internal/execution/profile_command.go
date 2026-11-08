package execution

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"whirlwind/internal/process"
	"whirlwind/internal/redaction"
)

// Command never starts the target directly. Missing runtime or unsupported hosts fail closed.
func Command(ctx context.Context, command []string, p Profile) (*exec.Cmd, error) {
	node, args, err := prepareSandboxCommand(command, p)
	if err != nil {
		return nil, err
	}
	cmd := process.Command(ctx, node, args...)
	cmd.Dir = p.CWD
	// Preserve process recovery token, but not ambient credentials or loader injection.
	cmd.Env = filteredEnvironment(cmd.Env, p)
	cmd.Env = append(cmd.Env, "PWD="+p.CWD)
	if openShellLauncher(p.Launcher) {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, fmt.Errorf("%w: cannot create per-sandbox proxy capability", ErrSandbox)
		}
		token := hex.EncodeToString(raw[:])
		var releases []func()
		var secrets []string
		register := func(secret string) error {
			release, err := redaction.RegisterSecret(secret, 365*24*time.Hour)
			if err != nil {
				return err
			}
			releases = append(releases, release)
			secrets = append(secrets, secret)
			return nil
		}
		if err := register(token); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSandbox, err)
		}
		if err := register(base64.StdEncoding.EncodeToString([]byte("whirlwind:" + token))); err != nil {
			for _, release := range releases {
				release()
			}
			return nil, fmt.Errorf("%w: %v", ErrSandbox, err)
		}
		if p.MCPBroker != nil {
			if err := register(p.MCPBrokerToken); err != nil {
				for _, release := range releases {
					release()
				}
				return nil, fmt.Errorf("%w: cannot register MCP broker capability for trace redaction: %v", ErrSandbox, err)
			}
		}
		for _, entry := range cmd.Env {
			name, value, _ := strings.Cut(entry, "=")
			if slices.Contains(p.Environment, name) && secretEnvironmentName(name) && value != "" {
				if len(value) < 16 {
					for _, release := range releases {
						release()
					}
					return nil, fmt.Errorf("%w: credential environment reference %s is too short to register safely", ErrSandbox, name)
				}
				if err := register(value); err != nil {
					for _, release := range releases {
						release()
					}
					return nil, fmt.Errorf("%w: %v", ErrSandbox, err)
				}
			}
		}
		if err := process.OnCleanup(cmd, func() {
			for _, release := range releases {
				release()
			}
		}); err != nil {
			for _, release := range releases {
				release()
			}
			return nil, fmt.Errorf("%w: %v", ErrSandbox, err)
		}
		if err := process.OnStart(cmd, func() error {
			for _, secret := range secrets {
				if err := redaction.KeepSecretUntilReleased(secret); err != nil {
					return fmt.Errorf("cannot retain runtime credential redaction: %w", err)
				}
			}
			return nil
		}); err != nil {
			for _, release := range releases {
				release()
			}
			return nil, fmt.Errorf("%w: %v", ErrSandbox, err)
		}
		cmd.Env = append(cmd.Env, "WHIRLWIND_OPENSHELL_PROXY_TOKEN="+token)
		if p.MCPBroker != nil {
			cmd.Env = append(cmd.Env, "WHIRLWIND_MCP_BROKER_TOKEN="+p.MCPBrokerToken)
		}
	}
	return cmd, nil
}

func secretEnvironmentName(name string) bool {
	name = strings.ToUpper(name)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "AUTH"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func openShellLauncher(launcher string) bool {
	return strings.Contains(filepath.ToSlash(launcher), "/bridges/openshell/")
}

func IsOpenShellLauncher(launcher string) bool { return openShellLauncher(launcher) }

func prepareSandboxCommand(command []string, profile Profile) (string, []string, error) {
	if err := validateCommandSetup(command, profile); err != nil {
		return "", nil, err
	}
	node, launcher, err := sandboxRuntime(profile)
	if err != nil {
		return "", nil, err
	}
	var target string
	if openShellLauncher(launcher) {
		// Host-resolved executable paths may point to macOS binaries that cannot run
		// in OpenShell's Linux image. Pass the configured launch name to that image.
		target = command[0]
	} else {
		target, err = commandTarget(command[0])
		if err != nil {
			return "", nil, err
		}
	}
	command = append([]string{target}, command[1:]...)
	args, err := commandArguments(launcher, profile, command)
	if err != nil {
		return "", nil, err
	}
	return node, args, nil
}

func validateCommandSetup(command []string, profile Profile) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return fmt.Errorf("%w: unsupported operating system", ErrSandbox)
	}
	if len(command) == 0 || profile.CWD == "" || profile.Launcher == "" {
		return fmt.Errorf("%w: command and execution profile required", ErrSandbox)
	}
	if err := validateMCPBroker(profile); err != nil {
		return err
	}
	return nil
}

func validateMCPBroker(profile Profile) error {
	if profile.MCPBroker == nil {
		if profile.MCPBrokerToken != "" {
			return fmt.Errorf("%w: MCP broker token supplied without a broker descriptor", ErrSandbox)
		}
		return nil
	}
	broker := profile.MCPBroker
	if broker.ID == "" || !filepath.IsAbs(broker.SocketPath) || broker.Host != "whirlwind-mcp.openshell.internal" || broker.Path != "/mcp" {
		return fmt.Errorf("%w: MCP broker descriptor must name the reserved host, exact /mcp path, and absolute Unix socket", ErrSandbox)
	}
	if len(profile.MCPBrokerToken) < 32 {
		return fmt.Errorf("%w: MCP broker capability is missing or too short", ErrSandbox)
	}
	if !openShellLauncher(profile.Launcher) {
		return fmt.Errorf("%w: MCP broker requires the OpenShell host proxy", ErrSandbox)
	}
	for _, root := range append([]string{profile.CWD, profile.RuntimeDir}, append(slices.Clone(profile.ReadRoots), profile.WriteRoots...)...) {
		if root != "" && pathInside(root, broker.SocketPath) {
			return fmt.Errorf("%w: MCP broker socket must remain outside sandbox mounts", ErrSandbox)
		}
	}
	for _, file := range profile.NativeFiles {
		if pathInside(file.Source, broker.SocketPath) || pathInside(broker.SocketPath, file.Source) {
			return fmt.Errorf("%w: MCP broker socket must remain outside sandbox mounts", ErrSandbox)
		}
	}
	return nil
}

func pathInside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func sandboxRuntime(profile Profile) (string, string, error) {
	launcher, err := filepath.EvalSymlinks(profile.Launcher)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrSandbox, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return "", "", fmt.Errorf("%w: Node.js unavailable: %v", ErrSandbox, err)
	}
	if _, err := openShellCLI(); err != nil {
		return "", "", fmt.Errorf("%w: NVIDIA OpenShell CLI unavailable: install and configure a healthy local gateway", ErrSandbox)
	}
	return node, launcher, nil
}

func commandTarget(name string) (string, error) {
	target, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	target = resolveSystemGit(target)
	return filepath.EvalSymlinks(target)
}

func sandboxArguments(launcher string, profile Profile, command []string) ([]string, error) {
	data, err := json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	return append([]string{launcher, string(data), "--"}, command...), nil
}

func commandArguments(launcher string, profile Profile, command []string) ([]string, error) {
	profile, err := ForCommand(profile, command)
	if err != nil {
		return nil, err
	}
	return sandboxArguments(launcher, profile, command)
}

func filteredEnvironment(environment []string, profile Profile) []string {
	var filtered []string
	openShell := openShellLauncher(profile.Launcher)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name != "WHIRLWIND_MCP_BROKER_TOKEN" && (strings.HasPrefix(name, "WHIRLWIND_PROCESS_") || slices.Contains([]string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM", "TMPDIR", "WHIRLWIND_OPENSHELL_IMAGE"}, name) || openShell && openShellBootstrapEnvironment(name) || slices.Contains(profile.Environment, name) && !(openShell && strings.HasPrefix(name, "OPENSHELL_"))) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func openShellBootstrapEnvironment(name string) bool {
	return slices.Contains([]string{"WHIRLWIND_OPENSHELL_CLI", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "OPENSHELL_GATEWAY", "OPENSHELL_GATEWAY_ENDPOINT", "OPENSHELL_WORKSPACE"}, name)
}

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"whirlwind/internal/process"
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
	if proxyLauncher(p.Launcher) {
		if err := registerProxyCapabilities(cmd, p); err != nil {
			return nil, err
		}
	}
	if err := attachNonoLifecycle(cmd, p); err != nil {
		_ = process.Forget(cmd)
		return nil, err
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

func IsNonoLauncher(launcher string) bool {
	return strings.Contains(filepath.ToSlash(launcher), "/bridges/nono/")
}

func proxyLauncher(launcher string) bool {
	return openShellLauncher(launcher) || IsNonoLauncher(launcher)
}

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
	if !proxyLauncher(profile.Launcher) {
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
	if openShellLauncher(launcher) {
		if _, err := openShellCLI(); err != nil {
			return "", "", fmt.Errorf("%w: NVIDIA OpenShell CLI unavailable: install and configure a healthy local gateway", ErrSandbox)
		}
	} else if _, err := nonoCLI(); err != nil {
		return "", "", err
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

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const openShellDiscoveryLimit = 10 * time.Second

var ErrIsolationUnverified = errors.New("sandbox error: isolation could not be verified")

type SandboxLifecycle struct {
	ActiveProcesses     int    `json:"active_processes,omitempty"`
	UpdatedAt           string `json:"updated_at"`
	ExecutionID         string `json:"execution_id"`
	DeadlineAt          string `json:"deadline_at"`
	ExecutionLabelValue string `json:"execution_label_value"`
	GatewayEndpoint     string `json:"gateway_endpoint"`
	GatewayName         string `json:"gateway_name"`
	Workspace           string `json:"workspace"`
	SandboxName         string `json:"sandbox_name"`
	SandboxID           string `json:"sandbox_id"`
	SandboxPhase        string `json:"sandbox_phase"`
	SandboxHealth       string `json:"sandbox_health"`
	ExecutionGeneration int    `json:"execution_generation"`
}

type openShellGateway struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Active   bool   `json:"active"`
	Remote   bool   `json:"is_remote"`
}

func PinOpenShellRuntime(parent context.Context, profile Profile, executionID string, generation int, deadline, supervisorDir string) (Profile, error) {
	if !validExecutionID(executionID) || generation < 1 {
		return Profile{}, fmt.Errorf("%w: execution identity is invalid", ErrSandbox)
	}
	deadlineAt, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil || !deadlineAt.After(time.Now()) {
		return Profile{}, fmt.Errorf("%w: execution deadline is invalid or expired", ErrSandbox)
	}
	name, endpoint, workspace, err := ResolveOpenShellGateway(parent)
	if err != nil {
		return Profile{}, err
	}
	dir, err := privateLifecycleDirectory(supervisorDir, executionID)
	if err != nil {
		return Profile{}, err
	}
	if lifecycleDirectoryMounted(dir, profile) {
		return Profile{}, fmt.Errorf("%w: lifecycle state must remain outside sandbox mounts", ErrSandbox)
	}
	profile.ExecutionID = executionID
	profile.ExecutionGeneration = generation
	profile.SandboxName = OpenShellSandboxName(executionID)
	profile.LifecycleFile = filepath.Join(dir, "openshell.json")
	profile.ExecutionLabelValue = OpenShellExecutionLabel(executionID, generation)
	profile.GatewayName = name
	profile.GatewayEndpoint = endpoint
	profile.Workspace = workspace
	profile.DeadlineAt = deadlineAt.UTC().Format(time.RFC3339Nano)
	if err := persistOpenShellIdentity(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func persistOpenShellIdentity(profile Profile) error {
	identity := SandboxLifecycle{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), ExecutionID: profile.ExecutionID,
		DeadlineAt: profile.DeadlineAt, ExecutionLabelValue: profile.ExecutionLabelValue,
		ExecutionGeneration: profile.ExecutionGeneration, SandboxName: profile.SandboxName,
		GatewayName: profile.GatewayName, GatewayEndpoint: profile.GatewayEndpoint,
		Workspace: profile.Workspace, SandboxPhase: "provisioning", SandboxHealth: "unknown",
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(profile.LifecycleFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		current, readErr := os.ReadFile(profile.LifecycleFile)
		var existing SandboxLifecycle
		if readErr == nil && json.Unmarshal(current, &existing) == nil && lifecycleIdentityMatches(existing, profile) {
			return nil
		}
		return fmt.Errorf("%w: existing lifecycle identity conflicts with the pinned execution", ErrIsolationUnverified)
	}
	if err != nil {
		return fmt.Errorf("%w: cannot persist prelaunch execution identity", ErrSandbox)
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr = errors.Join(writeErr, closeErr); writeErr != nil {
		_ = os.Remove(profile.LifecycleFile)
		return fmt.Errorf("%w: cannot persist prelaunch execution identity", ErrSandbox)
	}
	directory, err := os.Open(filepath.Dir(profile.LifecycleFile))
	if err != nil {
		return fmt.Errorf("%w: cannot sync prelaunch execution identity directory", ErrSandbox)
	}
	syncErr := directory.Sync()
	closeErr = directory.Close()
	if err = errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("%w: cannot sync prelaunch execution identity directory", ErrSandbox)
	}
	return nil
}

func ReadOpenShellLifecycle(profile Profile) (SandboxLifecycle, bool, error) {
	if profile.LifecycleFile == "" {
		return SandboxLifecycle{}, false, nil
	}
	data, err := os.ReadFile(profile.LifecycleFile)
	if errors.Is(err, os.ErrNotExist) {
		return SandboxLifecycle{}, false, nil
	}
	if err != nil {
		return SandboxLifecycle{}, false, fmt.Errorf("%w: cannot read lifecycle proof", ErrIsolationUnverified)
	}
	var lifecycle SandboxLifecycle
	if err := json.Unmarshal(data, &lifecycle); err != nil || !lifecycleMatchesProfile(lifecycle, profile) {
		return SandboxLifecycle{}, false, fmt.Errorf("%w: lifecycle proof does not match the persisted execution identity", ErrIsolationUnverified)
	}
	return lifecycle, true, nil
}

func lifecycleMatchesProfile(lifecycle SandboxLifecycle, profile Profile) bool {
	if !lifecycleIdentityMatches(lifecycle, profile) || lifecycle.SandboxPhase == "" {
		return false
	}
	return lifecycle.SandboxPhase == "provisioning" && lifecycle.SandboxID == "" && lifecycle.SandboxHealth == "unknown" || lifecycle.SandboxID != ""
}

func lifecycleIdentityMatches(lifecycle SandboxLifecycle, profile Profile) bool {
	return lifecycle.ExecutionID == profile.ExecutionID && lifecycle.ExecutionGeneration == profile.ExecutionGeneration &&
		lifecycle.ExecutionLabelValue == profile.ExecutionLabelValue && lifecycle.DeadlineAt == profile.DeadlineAt &&
		lifecycle.SandboxName == profile.SandboxName && lifecycle.GatewayName == profile.GatewayName &&
		lifecycle.GatewayEndpoint == profile.GatewayEndpoint && lifecycle.Workspace == profile.Workspace
}

func validExecutionID(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func OpenShellSandboxName(executionID string) string {
	sum := sha256.Sum256([]byte(executionID))
	return "ww-" + hex.EncodeToString(sum[:8])
}

func OpenShellExecutionLabel(executionID string, generation int) string {
	sum := sha256.Sum256([]byte(executionID))
	return hex.EncodeToString(sum[:12]) + "-" + fmt.Sprint(generation)
}

func privateLifecycleDirectory(root, executionID string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%w: lifecycle directory must be absolute", ErrSandbox)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("%w: cannot create private lifecycle directory", ErrSandbox)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%w: lifecycle root cannot be resolved", ErrSandbox)
	}
	root = resolved
	dir := filepath.Join(root, executionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("%w: cannot create private lifecycle directory", ErrSandbox)
	}
	for _, path := range []string{root, dir} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%w: lifecycle directory is not a real directory", ErrSandbox)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return "", fmt.Errorf("%w: cannot protect lifecycle directory", ErrSandbox)
		}
	}
	return dir, nil
}

func lifecycleDirectoryMounted(path string, profile Profile) bool {
	roots := []string{profile.CWD, profile.RuntimeDir}
	roots = append(roots, profile.ReadRoots...)
	roots = append(roots, profile.WriteRoots...)
	for _, file := range profile.NativeFiles {
		roots = append(roots, file.Source)
	}
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		if pathContains(root, path) || pathContains(path, root) {
			return true
		}
	}
	return false
}

func pathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// ResolveOpenShellGateway pins the selected local authority before sandbox
// creation, so later cleanup never follows a changed active-gateway setting.
func ResolveOpenShellGateway(parent context.Context) (string, string, string, error) {
	cli, err := openShellCLI()
	if err != nil {
		return "", "", "", err
	}
	ctx, cancel := context.WithTimeout(parent, openShellDiscoveryLimit)
	defer cancel()
	output, err := exec.CommandContext(ctx, cli, "gateway", "list", "--output", "json").Output()
	if err != nil {
		return "", "", "", fmt.Errorf("%w: cannot resolve the selected OpenShell gateway", ErrSandbox)
	}
	var gateways []openShellGateway
	if err := json.Unmarshal(output, &gateways); err != nil {
		return "", "", "", fmt.Errorf("%w: gateway list returned invalid JSON", ErrSandbox)
	}
	explicitName := strings.TrimSpace(os.Getenv("OPENSHELL_GATEWAY"))
	explicitEndpoint := strings.TrimSpace(os.Getenv("OPENSHELL_GATEWAY_ENDPOINT"))
	selected, err := selectedOpenShellGateway(gateways, explicitName, explicitEndpoint)
	if err != nil {
		return "", "", "", err
	}
	workspace := strings.TrimSpace(os.Getenv("OPENSHELL_WORKSPACE"))
	if workspace == "" {
		workspace = "default"
	}
	return selected.Name, selected.Endpoint, workspace, nil
}

func openShellCLI() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("WHIRLWIND_OPENSHELL_CLI")); configured != "" {
		cli, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("%w: configured NVIDIA OpenShell CLI is not executable", ErrSandbox)
		}
		return cli, nil
	}
	cli, err := exec.LookPath("openshell")
	if err != nil {
		return "", fmt.Errorf("%w: NVIDIA OpenShell CLI unavailable", ErrSandbox)
	}
	return cli, nil
}

func selectedOpenShellGateway(gateways []openShellGateway, explicitName, explicitEndpoint string) (openShellGateway, error) {
	if explicitName != "" || explicitEndpoint != "" {
		for _, gateway := range gateways {
			if explicitName != "" && gateway.Name != explicitName || explicitEndpoint != "" && gateway.Endpoint != explicitEndpoint {
				continue
			}
			if gateway.Remote || !loopbackEndpoint(gateway.Endpoint) {
				return openShellGateway{}, fmt.Errorf("%w: explicit OpenShell gateway must be local", ErrSandbox)
			}
			return gateway, nil
		}
		return openShellGateway{}, fmt.Errorf("%w: explicitly selected OpenShell gateway was not found", ErrSandbox)
	}
	var selected openShellGateway
	for _, gateway := range gateways {
		if !gateway.Active {
			continue
		}
		if selected.Name != "" {
			return openShellGateway{}, fmt.Errorf("%w: multiple active gateways are ambiguous", ErrSandbox)
		}
		selected = gateway
	}
	if selected.Name == "" || selected.Remote || !loopbackEndpoint(selected.Endpoint) {
		return openShellGateway{}, fmt.Errorf("%w: select one recognized local OpenShell gateway", ErrSandbox)
	}
	return selected, nil
}

func loopbackEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

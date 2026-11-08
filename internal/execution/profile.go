// Package execution owns the shared, fail-closed process sandbox boundary.
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
	"whirlwind/internal/model"
)

type NativeFile struct {
	Source string `json:"source"`
	Home   string `json:"home"`
	Path   string `json:"path"`
}
type MCPBroker struct {
	ID         string `json:"id"`
	SocketPath string `json:"socket_path"`
	Host       string `json:"host"`
	Path       string `json:"path"`
}
type Profile struct {
	MCPBrokerToken      string       `json:"-"`
	SupervisorDir       string       `json:"-"`
	StandaloneRuntime   bool         `json:"-"`
	ExecutionID         string       `json:"execution_id,omitempty"`
	SandboxName         string       `json:"sandbox_name,omitempty"`
	SandboxID           string       `json:"sandbox_id,omitempty"`
	LifecycleFile       string       `json:"lifecycle_file,omitempty"`
	GatewayName         string       `json:"gateway_name,omitempty"`
	GatewayEndpoint     string       `json:"gateway_endpoint,omitempty"`
	Workspace           string       `json:"workspace,omitempty"`
	DeadlineAt          string       `json:"deadline_at,omitempty"`
	ExecutionGeneration int          `json:"execution_generation,omitempty"`
	ExecutionLabelValue string       `json:"execution_label_value,omitempty"`
	NativeConfigDigest  string       `json:"native_config_digest,omitempty"`
	NativeFiles         []NativeFile `json:"native_files,omitempty"`
	MCPBroker           *MCPBroker   `json:"mcp_broker,omitempty"`
	Version             string       `json:"version"`
	CommandHash         string       `json:"command_hash,omitempty"`
	AllowedPaths        []string     `json:"allowed_paths,omitempty"`
	CWD                 string       `json:"cwd"`
	ReadOnly            bool         `json:"read_only"`
	PolicyHash          string       `json:"policy_hash"`
	ReadRoots           []string     `json:"read_roots"`
	WriteRoots          []string     `json:"write_roots,omitempty"`
	Domains             []string     `json:"domains"`
	Environment         []string     `json:"environment"`
	Launcher            string       `json:"-"`
	RuntimeDir          string       `json:"runtime_dir,omitempty"`
	Sockets             []string     `json:"sockets,omitempty"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var ErrSandbox = errors.New("sandbox setup required")

func ForPolicy(cwd string, readOnly bool, hash string, policy *model.SandboxPolicy) (Profile, error) {
	root, err := boundedWorktreeRoot(cwd)
	if err != nil {
		return Profile{}, err
	}
	p := Profile{Version: "srt-0.0.81-checks-v1", CWD: root, ReadOnly: readOnly, PolicyHash: hash, ReadRoots: []string{}, Domains: []string{}, Environment: []string{}}
	if err := applyPolicy(&p, policy); err != nil {
		return Profile{}, err
	}
	for _, values := range [][]string{p.ReadRoots, p.Domains, p.Environment} {
		slices.Sort(values)
	}
	p.Launcher = defaultLauncher()
	// Linked worktrees keep object/index metadata outside their source directory.
	// Grant those Git metadata directories read-only, never their parent repository.
	p.ReadRoots = append(p.ReadRoots, gitMetadataReadRoots(root)...)
	slices.Sort(p.ReadRoots)
	return p, nil
}

func boundedWorktreeRoot(cwd string) (string, error) {
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil || root == string(filepath.Separator) {
		return "", errors.New("sandbox requires a bounded worktree")
	}
	return root, nil
}

func applyPolicy(profile *Profile, policy *model.SandboxPolicy) error {
	if policy == nil {
		return nil
	}
	readRoots, err := policyReadRoots(policy.ReadRoots)
	if err != nil {
		return err
	}
	environment, err := policyEnvironment(policy.Environment)
	if err != nil {
		return err
	}
	profile.ReadRoots = append(profile.ReadRoots, readRoots...)
	profile.Domains = append(profile.Domains, policy.AllowedDomains...)
	profile.Environment = append(profile.Environment, environment...)
	return nil
}

func policyReadRoots(paths []string) ([]string, error) {
	roots := make([]string, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) == "/" || strings.ContainsAny(path, "*?[]{}\x00\n") {
			return nil, fmt.Errorf("sandbox read root must be an absolute literal bounded path: %q", path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("sandbox read root: %w", err)
		}
		if resolved == "/" {
			return nil, errors.New("sandbox read root resolves to filesystem root")
		}
		roots = append(roots, resolved)
	}
	return roots, nil
}

func policyEnvironment(names []string) ([]string, error) {
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		if !envName.MatchString(name) || unsafeEnvironment(name) {
			return nil, fmt.Errorf("unsafe sandbox environment exception: %s", name)
		}
		allowed = append(allowed, name)
	}
	return allowed, nil
}

func gitMetadataReadRoots(root string) []string {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	git = resolveSystemGit(git)
	var roots []string
	for _, option := range []string{"--absolute-git-dir", "--git-common-dir"} {
		queryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		value, err := exec.CommandContext(queryCtx, git, "-C", root, "rev-parse", option).Output()
		cancel()
		if err != nil {
			continue
		}
		path := strings.TrimSpace(string(value))
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "/" {
			roots = append(roots, resolved)
		}
	}
	return roots
}

func resolveSystemGit(path string) string {
	if runtime.GOOS == "darwin" && path == "/usr/bin/git" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "/usr/bin/xcrun", "--find", "git").Output(); err == nil {
			actual := strings.TrimSpace(string(out))
			if filepath.IsAbs(actual) {
				return actual
			}
		}
	}
	return path
}

func (p Profile) Digest() string {
	p.MCPBrokerToken = ""
	p.SupervisorDir = ""
	p.StandaloneRuntime = false
	p.ExecutionID = ""
	p.SandboxName = ""
	p.SandboxID = ""
	p.LifecycleFile = ""
	p.LifecycleFile = ""
	p.GatewayName = ""
	p.GatewayEndpoint = ""
	p.Workspace = ""
	p.DeadlineAt = ""
	p.ExecutionGeneration = 0
	p.ExecutionLabelValue = ""
	p.Launcher = ""
	p.RuntimeDir = ""
	p.Sockets = nil
	p.ReadRoots = slices.Clone(p.ReadRoots)
	slices.Sort(p.ReadRoots)
	p.ReadRoots = slices.Compact(p.ReadRoots)
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ForCommand resolves the exact program read grant that Command will enforce.
func ForCommand(p Profile, command []string) (Profile, error) {
	if len(command) == 0 {
		return p, fmt.Errorf("%w: command required", ErrSandbox)
	}
	if openShellLauncher(p.Launcher) {
		b, _ := json.Marshal(command)
		sum := sha256.Sum256(b)
		p.CommandHash = hex.EncodeToString(sum[:])
		return p, nil
	}
	target, err := exec.LookPath(command[0])
	if err != nil {
		return p, err
	}
	target, err = filepath.EvalSymlinks(resolveSystemGit(target))
	if err != nil {
		return p, err
	}
	p.ReadRoots = append(slices.Clone(p.ReadRoots), target)
	b, _ := json.Marshal(append([]string{target}, command[1:]...))
	sum := sha256.Sum256(b)
	p.CommandHash = hex.EncodeToString(sum[:])
	return p, nil
}

// ForProvider adds only the configured program's installed code roots, never implicit auth/network grants.
func defaultLauncher() string {
	if binary, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(binary), "bridges", "openshell", "launch.mjs")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "bridges", "openshell", "launch.mjs")
}

func unsafeEnvironment(name string) bool {
	if strings.HasPrefix(name, "WHIRLWIND_") || strings.HasPrefix(name, "OPENSHELL_") || strings.HasPrefix(name, "DYLD_") || strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "XDG_") {
		return true
	}
	return slices.Contains([]string{"PATH", "HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "PWD", "TMPDIR", "TMP", "TEMP", "OPENSSL_CONF", "OPENSSL_MODULES", "SSLKEYLOGFILE", "NODE_OPTIONS", "NODE_PATH", "BASH_ENV", "ENV", "SHELLOPTS", "PYTHONPATH", "PYTHONSTARTUP", "GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"}, name)
}

// EnvironmentOverrideAllowed keeps provider-controlled values out of the host runtime bootstrap.
func EnvironmentOverrideAllowed(name string, policy *model.SandboxPolicy) bool {
	if !envName.MatchString(name) || unsafeEnvironment(name) {
		return false
	}
	if slices.Contains([]string{"LANG", "LC_ALL", "TERM"}, name) {
		return true
	}
	return policy != nil && slices.Contains(policy.Environment, name)
}

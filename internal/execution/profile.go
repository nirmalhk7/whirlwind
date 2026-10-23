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
	"whirlwind/internal/process"
)

type NativeFile struct {
	Source string `json:"source"`
	Home   string `json:"home"`
	Path   string `json:"path"`
}
type Profile struct {
	NativeConfigDigest string       `json:"native_config_digest,omitempty"`
	NativeFiles        []NativeFile `json:"native_files,omitempty"`
	Version            string       `json:"version"`
	CommandHash        string       `json:"command_hash,omitempty"`
	AllowedPaths       []string     `json:"allowed_paths,omitempty"`
	CWD                string       `json:"cwd"`
	ReadOnly           bool         `json:"read_only"`
	PolicyHash         string       `json:"policy_hash"`
	ReadRoots          []string     `json:"read_roots"`
	WriteRoots         []string     `json:"write_roots,omitempty"`
	Domains            []string     `json:"domains"`
	Environment        []string     `json:"environment"`
	Launcher           string       `json:"-"`
	RuntimeDir         string       `json:"runtime_dir,omitempty"`
	Sockets            []string     `json:"sockets,omitempty"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var ErrSandbox = errors.New("sandbox setup required")

func ForPolicy(cwd string, readOnly bool, hash string, policy *model.SandboxPolicy) (Profile, error) {
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return Profile{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil || root == string(filepath.Separator) {
		return Profile{}, errors.New("sandbox requires a bounded worktree")
	}
	p := Profile{Version: "srt-0.0.81-checks-v1", CWD: root, ReadOnly: readOnly, PolicyHash: hash, ReadRoots: []string{}, Domains: []string{}, Environment: []string{}}
	if policy != nil {
		for _, path := range policy.ReadRoots {
			if !filepath.IsAbs(path) || filepath.Clean(path) == "/" || strings.ContainsAny(path, "*?[]{}\x00\n") {
				return Profile{}, fmt.Errorf("sandbox read root must be an absolute literal bounded path: %q", path)
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return Profile{}, fmt.Errorf("sandbox read root: %w", err)
			}
			if resolved == "/" {
				return Profile{}, errors.New("sandbox read root resolves to filesystem root")
			}
			p.ReadRoots = append(p.ReadRoots, resolved)
		}
		p.Domains = append(p.Domains, policy.AllowedDomains...)
		for _, name := range policy.Environment {
			if !envName.MatchString(name) || unsafeEnvironment(name) {
				return Profile{}, fmt.Errorf("unsafe sandbox environment exception: %s", name)
			}
			p.Environment = append(p.Environment, name)
		}
	}
	for _, values := range [][]string{p.ReadRoots, p.Domains, p.Environment} {
		slices.Sort(values)
	}
	p.Launcher = defaultLauncher()
	// Linked worktrees keep object/index metadata outside their source directory.
	// Grant those Git metadata directories read-only, never their parent repository.
	git, gitErr := exec.LookPath("git")
	if gitErr == nil {
		git = resolveSystemGit(git)
		for _, option := range []string{"--absolute-git-dir", "--git-common-dir"} {
			queryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			value, e := exec.CommandContext(queryCtx, git, "-C", root, "rev-parse", option).Output()
			cancel()
			if e != nil {
				continue
			}
			path := strings.TrimSpace(string(value))
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			if resolved, e := filepath.EvalSymlinks(path); e == nil && resolved != "/" {
				p.ReadRoots = append(p.ReadRoots, resolved)
			}
		}
		slices.Sort(p.ReadRoots)
	}
	return p, nil
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
func ForProvider(cwd string, readOnly bool, hash string, policy *model.SandboxPolicy, command []string) (Profile, error) {
	p, err := ForPolicy(cwd, readOnly, hash, policy)
	if err != nil {
		return p, err
	}
	b, _ := json.Marshal(command)
	sum := sha256.Sum256(b)
	p.CommandHash = hex.EncodeToString(sum[:])
	// Missing programs still get a profile identity; the launch boundary refuses them.
	if len(command) == 0 {
		return p, nil
	}
	executable, err := exec.LookPath(command[0])
	if err != nil {
		return p, nil
	}
	paths := []string{executable}
	for _, arg := range command[1:] {
		if filepath.IsAbs(arg) {
			if info, e := os.Stat(arg); e == nil && info.Mode().IsRegular() {
				paths = append(paths, arg)
			}
		}
	}
	for _, path := range paths {
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			return p, e
		}
		p.ReadRoots = append(p.ReadRoots, resolved)
		switch filepath.Ext(resolved) {
		case ".js", ".mjs", ".cjs":
			dir := filepath.Dir(resolved)
			for depth := 0; depth < 4; depth++ {
				if _, e := os.Stat(filepath.Join(dir, "package.json")); e == nil {
					p.ReadRoots = append(p.ReadRoots, dir)
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
	}
	slices.Sort(p.ReadRoots)
	return ForCommand(p, command)
}

// ForCheck grants declared build directories without making source or Git metadata writable.
func ForCheck(p Profile, outputs []string) (Profile, error) {
	p.ReadOnly = true
	p.WriteRoots = nil
	for _, path := range outputs {
		clean := filepath.Clean(path)
		if !filepath.IsLocal(clean) || clean == "." || clean == ".git" || strings.HasPrefix(clean, ".git/") || strings.HasPrefix(clean, ".whirlwind.") {
			return p, fmt.Errorf("unsafe check output path: %s", path)
		}
		target := filepath.Join(p.CWD, clean)
		if err := filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
				return errors.New("check outputs must be fresh directories, not existing source files or links")
			}
			return nil
		}); err != nil {
			return p, err
		}
		prefix := target
		for {
			resolved, err := filepath.EvalSymlinks(prefix)
			if err == nil {
				rel, e := filepath.Rel(p.CWD, resolved)
				if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
					return p, errors.New("check output symlink escapes snapshot")
				}
				break
			}
			if !os.IsNotExist(err) {
				return p, err
			}
			parent := filepath.Dir(prefix)
			if parent == prefix {
				return p, errors.New("check output cannot be resolved")
			}
			prefix = parent
		}
		p.WriteRoots = append(p.WriteRoots, target)
	}
	return p, nil
}

func defaultLauncher() string {
	if binary, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(binary), "bridges", "sandbox", "launch.mjs")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "bridges", "sandbox", "launch.mjs")
}

func unsafeEnvironment(name string) bool {
	return strings.HasPrefix(name, "WHIRLWIND_") || strings.HasPrefix(name, "DYLD_") || strings.HasPrefix(name, "LD_") || slices.Contains([]string{"PATH", "HOME", "PWD", "TMPDIR", "TMP", "TEMP", "OPENSSL_CONF", "OPENSSL_MODULES", "SSLKEYLOGFILE", "NODE_OPTIONS", "NODE_PATH", "BASH_ENV", "ENV", "SHELLOPTS", "PYTHONPATH", "PYTHONSTARTUP", "GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"}, name)
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

// Command never starts the target directly. Missing runtime or unsupported hosts fail closed.
func Command(ctx context.Context, command []string, p Profile) (*exec.Cmd, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("%w: unsupported operating system", ErrSandbox)
	}
	if len(command) == 0 || p.CWD == "" || p.Launcher == "" {
		return nil, fmt.Errorf("%w: command and execution profile required", ErrSandbox)
	}
	launcher, err := filepath.EvalSymlinks(p.Launcher)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSandbox, err)
	}
	installed, err := os.ReadFile(filepath.Join(filepath.Dir(launcher), "node_modules", "@anthropic-ai", "sandbox-runtime", "package.json"))
	var version struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(installed, &version) != nil || version.Version != "0.0.81" {
		return nil, fmt.Errorf("%w: install pinned dependency with npm ci --prefix %s", ErrSandbox, filepath.Dir(launcher))
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("%w: Node.js unavailable: %v", ErrSandbox, err)
	}
	target, err := exec.LookPath(command[0])
	if err != nil {
		return nil, err
	}
	target = resolveSystemGit(target)
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return nil, err
	}
	command = append([]string{target}, command[1:]...)
	p, err = ForCommand(p, command)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	args := append([]string{launcher, string(b), "--"}, command...)
	cmd := process.Command(ctx, node, args...)
	cmd.Dir = p.CWD
	// Preserve process recovery token, but not ambient credentials or loader injection.
	var env []string
	for _, entry := range cmd.Env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "WHIRLWIND_PROCESS_") || slices.Contains([]string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM", "TMPDIR"}, name) || slices.Contains(p.Environment, name) {
			env = append(env, entry)
		}
	}
	cmd.Env = env
	cmd.Env = append(cmd.Env, "PWD="+p.CWD)
	return cmd, nil
}

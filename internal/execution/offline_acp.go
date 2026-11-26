package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

const (
	offlineACPNamePrefix = "ww-offline-acp-"
	offlineACPOwnerLabel = "io.whirlwind.offline-acp.owner"
	offlineACPProgram    = "/whirlwind-fixture"
	offlineACPVersion    = "offline-acp-fixture-v1"
)

type offlineACPProfile struct {
	image, project, cwd, program, programSHA string
}

func (p Profile) OfflineACP() bool { return p.offlineACP != nil }

// selectOfflineACP is intentionally limited to a separately configured
// read-only fixture, one checkout, and an operator-selected executable.
func (p *Profile) selectOfflineACP(command []string, policy *model.SandboxPolicy) error {
	program := os.Getenv("WHIRLWIND_OFFLINE_ACP_FIXTURE")
	if program == "" || len(command) == 0 || command[0] != program {
		return nil
	}
	if !p.direct || runtime.GOOS != "linux" || len(command) != 1 || !filepath.IsAbs(program) || !p.ReadOnly || policy != nil {
		return fmt.Errorf("%w: offline ACP requires explicit direct mode and a read-only fixture without sandbox grants", ErrSandbox)
	}
	project, image := os.Getenv("WHIRLWIND_OFFLINE_ACP_CHECKOUT"), os.Getenv("WHIRLWIND_OFFLINE_ACP_IMAGE")
	registry := os.Getenv("WHIRLWIND_PROCESS_REGISTRY")
	scratch := filepath.Join(filepath.Dir(registry), "ideation")
	relative, relationErr := filepath.Rel(scratch, p.CWD)
	inScratch := filepath.IsAbs(registry) && relationErr == nil && filepath.IsLocal(relative) && relative != "."
	if !filepath.IsAbs(project) || !model.IsOfflineDockerImage(image) || (p.CWD != project && !inScratch) {
		return fmt.Errorf("%w: offline ACP requires the designated checkout and locally pinned image", ErrSandbox)
	}
	resolved, err := filepath.EvalSymlinks(program)
	if err != nil || resolved != program || (pathsOverlap(project, program) || pathsOverlap(p.CWD, program)) {
		return fmt.Errorf("%w: offline ACP fixture must be outside the checkout", ErrSandbox)
	}
	digest, err := offlineACPProgramDigest(program)
	if err != nil {
		return errors.Join(ErrSandbox, err)
	}
	p.offlineACP = &offlineACPProfile{image: image, project: project, cwd: p.CWD, program: program, programSHA: digest}
	p.Version = offlineACPVersion + ":" + image + ":" + digest
	return nil
}

func offlineACPProgramDigest(path string) (string, error) {
	const maxProgram = 64 << 20
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 || info.Mode().Perm()&0022 != 0 || info.Size() > maxProgram {
		return "", errors.New("offline ACP fixture must be a bounded, executable regular file controlled by the operator")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maxProgram+1))
	if err != nil || count > maxProgram {
		return "", errors.Join(errors.New("could not hash the offline ACP fixture"), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func pathsOverlap(a, b string) bool {
	ab, err1 := filepath.Rel(a, b)
	ba, err2 := filepath.Rel(b, a)
	return err1 != nil || err2 != nil || filepath.IsLocal(ab) || filepath.IsLocal(ba)
}

func OfflineACPRegistry(data string) string { return filepath.Join(data, "offline-acp") }

type offlineACPRecord struct {
	Name   string `json:"name"`
	Token  string `json:"token"`
	Image  string `json:"image"`
	Daemon string `json:"daemon"`
	ID     string `json:"id,omitempty"`
}

func (r offlineACPRecord) valid() bool {
	token, err := hex.DecodeString(r.Token)
	return err == nil && len(token) == 32 && r.Name == offlineACPNamePrefix+r.Token[:24] &&
		validOfflineACPDaemon(r.Daemon) && IsOfflineDockerImageID(r.Image) && (r.ID == "" || dockerID.MatchString(r.ID))
}

type OfflineACPConnection struct {
	io.ReadWriteCloser
	docker *offlineACPAPI
	root   string
	record offlineACPRecord
	lock   *os.File
}

func (p Profile) OpenOfflineACP(ctx context.Context, command []string) (_ *OfflineACPConnection, failure error) {
	if p.offlineACP == nil || !p.direct || !p.ReadOnly || !slices.Equal(command, []string{p.offlineACP.program}) ||
		len(p.WriteRoots)+len(p.Credentials)+len(p.Environment)+len(p.NativeFiles)+len(p.ProtectedFiles)+len(p.Sockets) != 0 ||
		p.LifecycleHook != nil || p.MCPBroker != nil || p.MCPBrokerToken != "" {
		return nil, fmt.Errorf("%w: offline ACP configuration exceeds fixture grants", ErrSandbox)
	}
	if enabled, err := SandboxEnabled(); err != nil || enabled {
		return nil, errors.Join(ErrSandbox, errors.New("offline ACP requires WHIRLWIND_SANDBOX=off"), err)
	}
	if p.CWD != p.offlineACP.cwd {
		return nil, errors.New("offline ACP worktree changed after profile selection")
	}
	registry := os.Getenv("WHIRLWIND_PROCESS_REGISTRY")
	if !filepath.IsAbs(registry) {
		return nil, errors.New("offline ACP requires the supervisor's private process registry")
	}
	root := OfflineACPRegistry(filepath.Dir(registry))
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root || pathsOverlap(root, p.CWD) || pathsOverlap(root, p.offlineACP.program) {
		return nil, errors.New("offline ACP registry must be outside the fixture mounts")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errors.New("offline ACP requires a private lifecycle registry")
	}
	lock, err := process.Lock(root + ".lock")
	if err != nil {
		return nil, errors.New("another offline ACP worker owns the registry")
	}
	defer func() {
		if failure != nil {
			_ = lock.Close()
		}
	}()
	if err := CheckOfflineACPRecovery(root); err != nil {
		return nil, err
	}
	if err := checkOfflineACPCheckout(ctx, p.offlineACP.project, p.CWD); err != nil {
		return nil, err
	}
	if digest, err := offlineACPProgramDigest(p.offlineACP.program); err != nil || digest != p.offlineACP.programSHA {
		return nil, errors.Join(ErrIsolationUnverified, errors.New("offline ACP executable changed after profile selection"), err)
	}
	api, err := openOfflineACPAPI(ctx)
	if err != nil {
		return nil, err
	}
	image, err := api.image(ctx, p.offlineACP.image)
	if err != nil {
		return nil, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	owner := hex.EncodeToString(token)
	r := offlineACPRecord{Name: offlineACPNamePrefix + owner[:24], Token: owner, Image: image, Daemon: api.daemon}
	if err := saveOfflineACPRecord(root, r); err != nil {
		return nil, err
	}
	connection, err := api.start(ctx, root, r, *p.offlineACP)
	if err != nil {
		return nil, err
	}
	connection.lock = lock
	return connection, nil
}

func checkOfflineACPCheckout(ctx context.Context, checkout, cwd string) error {
	canonical, err := filepath.EvalSymlinks(checkout)
	if err != nil || canonical != checkout {
		return errors.New("offline ACP requires a canonical disposable checkout")
	}
	info, err := os.Lstat(filepath.Join(checkout, ".git"))
	if err != nil || !info.IsDir() {
		return errors.New("offline ACP requires a separate disposable clone")
	}
	remotes, err := runOfflineACPGit(ctx, checkout, "remote")
	if err != nil || remotes != "" {
		return errors.Join(errors.New("offline ACP checkout must have no Git remotes"), err)
	}
	if cwd != checkout {
		common, err := runOfflineACPGit(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || common != filepath.Join(checkout, ".git") {
			return errors.Join(errors.New("offline ACP scratch is not owned by the disposable checkout"), err)
		}
	}
	return nil
}

func runOfflineACPGit(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := process.Command(ctx, "git", append([]string{"-C", cwd}, args...)...)
	cmd.Env = filteredOfflineGitEnvironment(cmd.Env)
	var out strings.Builder
	cmd.Stdout = &out
	err := process.Run(cmd)
	return strings.TrimSpace(out.String()), err
}

func filteredOfflineGitEnvironment(inherited []string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	for _, entry := range inherited {
		if strings.HasPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=") {
			env = append(env, entry)
		}
	}
	return env
}

func saveOfflineACPRecord(root string, r offlineACPRecord) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(root, r.Name+".json")); err != nil {
		return err
	}
	return syncOfflineACPRegistry(root)
}

func syncOfflineACPRegistry(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// RecoverOfflineACP runs under the startup state lock. An unowned object or
// inaccessible daemon blocks startup and leaves the recovery record intact.
func RecoverOfflineACP(ctx context.Context, root string) error {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("offline ACP recovery requires a private registry")
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) == 0 {
		return err
	}
	api, err := openOfflineACPAPI(ctx)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), ".json") {
			return errors.New("unexpected file in offline ACP recovery registry")
		}
		path := filepath.Join(root, file.Name())
		raw, err := os.ReadFile(path)
		var record offlineACPRecord
		if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &record) != nil || !record.valid() || file.Name() != record.Name+".json" {
			return errors.New("invalid offline ACP recovery record")
		}
		if _, err := api.reap(ctx, root, record, true); err != nil {
			return err
		}
	}
	return nil
}

func CheckOfflineACPRecovery(root string) error {
	files, err := os.ReadDir(root)
	if os.IsNotExist(err) || err == nil && len(files) == 0 {
		return nil
	}
	return errors.Join(ErrIsolationUnverified, errors.New("offline ACP registry requires supervisor recovery"), err)
}

func (conn *OfflineACPConnection) VerifyCleanup() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	defer func() { _ = conn.lock.Close() }()
	code, err := conn.docker.reap(ctx, conn.root, conn.record, false)
	if err != nil {
		return -1, errors.Join(process.ErrCleanup, err)
	}
	return code, nil
}

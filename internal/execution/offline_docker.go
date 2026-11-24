package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"whirlwind/internal/process"
)

const OfflineDockerKind = "docker-offline"

const offlineDockerSocket = "/var/run/docker.sock"
const offlineDockerLabel = "io.whirlwind.offline-verifier"
const offlineDockerScratch = "rw,exec,nodev,nosuid,size=256m"

var offlineImageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
var dockerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var dockerImageID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func IsOfflineDockerImage(reference string) bool { return offlineImageRef.MatchString(reference) }

func IsOfflineDockerIdentity(id, imageID string) bool {
	return dockerID.MatchString(id) && dockerImageID.MatchString(imageID)
}

type OfflineDocker struct {
	binary  string
	config  string
	imageID string
}

func OpenOfflineDocker(ctx context.Context, config, image string) (*OfflineDocker, error) {
	if !IsOfflineDockerImage(image) || os.Geteuid() == 0 {
		return nil, errors.New("offline verifier requires a digest-pinned image and a non-root operator")
	}
	if info, err := os.Stat(offlineDockerSocket); err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("managed local Docker socket is unavailable")
	}
	binary, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("offline verifier requires the host Docker CLI")
	}
	d := &OfflineDocker{binary: binary, config: config}
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var security []string
	if err := d.inspect(probe, &security, "info", "--format", "{{json .SecurityOptions}}"); err != nil {
		return nil, err
	}
	if !slices.Contains(security, "name=rootless") || !slices.Contains(security, "name=seccomp,profile=builtin") {
		return nil, errors.New("offline verifier requires rootless Docker with the built-in seccomp profile")
	}
	var images []struct {
		ID          string
		Os          string
		RepoDigests []string
		Config      struct{ Volumes map[string]any }
	}
	if err := d.inspect(probe, &images, "image", "inspect", image); err != nil {
		return nil, fmt.Errorf("offline verifier image must already be present locally: %w", err)
	}
	if len(images) != 1 || !dockerImageID.MatchString(images[0].ID) || images[0].Os != "linux" || !slices.Contains(images[0].RepoDigests, image) || len(images[0].Config.Volumes) != 0 {
		return nil, errors.New("offline verifier requires the exact local Linux image digest and an image without declared volumes")
	}
	d.imageID = images[0].ID
	return d, nil
}

func (d *OfflineDocker) ImageID() string { return d.imageID }

// Run uses one container per command. The only bind mount is the read-only
// frozen export. The CLI operates outside the container with a private config.
func (d *OfflineDocker) Run(ctx context.Context, key, source string, argv []string, timeout time.Duration, stdout, stderr io.Writer) (id string, exit *int, cleaned bool, err error) {
	if strings.ContainsAny(source, ",\"\r\n") {
		return "", nil, false, errors.New("offline verifier checkout has an unsupported mount path")
	}
	if err := ctx.Err(); err != nil {
		return "", nil, false, err
	}
	createConfirmed := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		cleanupErr := d.Recover(cleanup, key)
		cleaned = createConfirmed && cleanupErr == nil
		err = errors.Join(err, cleanupErr)
	}()
	args := []string{"create", "--name", key, "--label", offlineDockerLabel + "=" + key, "--pull=never",
		"--runtime=runc", "--network=none", "--ipc=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--security-opt=seccomp=builtin", "--no-healthcheck",
		"--memory=768m", "--cpus=2", "--pids-limit=128", "--log-driver=none",
		"--user", fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid()),
		"--env", "HOME=/tmp", "--env", "GOCACHE=/tmp/go-build", "--env", "GOTOOLCHAIN=local",
		// Compilers can execute their test binaries in private, nonpersistent scratch.
		"--tmpfs", "/tmp:" + offlineDockerScratch,
		"--mount", "type=bind,src=" + source + ",dst=/checkout,readonly,bind-propagation=rprivate,bind-recursive=disabled",
		"--workdir", "/checkout", "--entrypoint", argv[0], d.imageID}
	// Cancellation of the CLI does not cancel an in-flight daemon create.
	// Wait for its response separately before starting the workload clock.
	creation, cancelCreation := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	created, err := d.read(creation, append(args, argv[1:]...)...)
	cancelCreation()
	if err != nil {
		return "", nil, false, fmt.Errorf("create confined check: %w", err)
	}
	id = strings.TrimSpace(string(created))
	if !dockerID.MatchString(id) {
		return "", nil, false, errors.New("Docker create returned no full container identity")
	}
	createConfirmed = true
	inspection, cancelInspection := context.WithTimeout(ctx, 15*time.Second)
	defer cancelInspection()
	if err := d.verifyContainer(inspection, id, key, source, argv); err != nil {
		return id, nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return id, nil, false, err
	}
	workload, cancelWorkload := context.WithTimeout(ctx, timeout)
	start := d.command(workload, "start", "--attach", id)
	start.Stdout, start.Stderr = stdout, stderr
	startErr := process.Run(start)
	workloadErr := workload.Err()
	cancelWorkload()
	if workloadErr != nil {
		return id, nil, false, workloadErr
	}
	var state struct {
		Status    string
		ExitCode  int
		Error     string
		OOMKilled bool
	}
	outcome, cancelOutcome := context.WithTimeout(ctx, 15*time.Second)
	defer cancelOutcome()
	if err := d.inspect(outcome, &state, "container", "inspect", "--format", "{{json .State}}", id); err != nil {
		return id, nil, false, err
	}
	if state.Status != "exited" || state.Error != "" || state.OOMKilled || state.ExitCode < 0 || state.ExitCode > 255 {
		return id, nil, false, errors.New("offline check has no completed container process outcome")
	}
	var exited *exec.ExitError
	if (state.ExitCode == 0 && startErr != nil) || (state.ExitCode != 0 && (!errors.As(startErr, &exited) || exited.ExitCode() != state.ExitCode)) {
		return id, nil, false, errors.New("Docker attach outcome disagrees with the container exit status")
	}
	return id, &state.ExitCode, false, nil
}

func (d *OfflineDocker) verifyContainer(ctx context.Context, id, key, source string, argv []string) error {
	var values []struct {
		Name, Image string
		Config      struct {
			User, WorkingDir string
			Entrypoint, Cmd  []string
			Labels           map[string]string
		}
		HostConfig struct {
			Runtime, NetworkMode, IpcMode string
			Privileged, ReadonlyRootfs    bool
			CapDrop, SecurityOpt          []string
			Tmpfs                         map[string]string
			Mounts                        []struct {
				Source, Target string
				ReadOnly       bool
				BindOptions    struct {
					Propagation  string
					NonRecursive bool
				}
			}
		}
		Mounts []struct {
			Type, Source, Destination string
			RW                        bool
		}
	}
	if err := d.inspect(ctx, &values, "container", "inspect", id); err != nil {
		return err
	}
	if len(values) != 1 {
		return errors.New("offline verifier container identity could not be established")
	}
	v := values[0]
	host := v.HostConfig
	if v.Name != "/"+key || v.Image != d.imageID || v.Config.Labels[offlineDockerLabel] != key || v.Config.User != fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid()) ||
		v.Config.WorkingDir != "/checkout" || !slices.Equal(v.Config.Entrypoint, argv[:1]) || !slices.Equal(v.Config.Cmd, argv[1:]) ||
		host.Runtime != "runc" || host.NetworkMode != "none" || host.IpcMode != "none" || host.Privileged || !host.ReadonlyRootfs ||
		!slices.Contains(host.CapDrop, "ALL") || !slices.Contains(host.SecurityOpt, "no-new-privileges") || !slices.Contains(host.SecurityOpt, "seccomp=builtin") ||
		len(host.Tmpfs) != 1 || host.Tmpfs["/tmp"] != offlineDockerScratch {
		return errors.New("Docker did not retain the requested offline verifier restrictions")
	}
	if len(host.Mounts) != 1 || host.Mounts[0].Source != source || host.Mounts[0].Target != "/checkout" || !host.Mounts[0].ReadOnly ||
		!host.Mounts[0].BindOptions.NonRecursive || host.Mounts[0].BindOptions.Propagation != "rprivate" {
		return errors.New("offline verifier requires a private, nonrecursive read-only bind")
	}
	bound := false
	for _, mount := range v.Mounts {
		if mount.Type == "tmpfs" && mount.Destination == "/tmp" {
			continue
		}
		if bound || mount.Type != "bind" || mount.Source != source || mount.Destination != "/checkout" || mount.RW {
			return errors.New("offline verifier container has an unexpected host mount")
		}
		bound = true
	}
	if !bound {
		return errors.New("offline verifier container lacks the read-only checkout")
	}
	return nil
}

// Recover removes only a container whose full name and ownership label agree.
// A positive daemon response after removal is required before retaining a pass.
func (d *OfflineDocker) Recover(ctx context.Context, key string) error {
	id, err := d.find(ctx, key)
	if err != nil || id == "" {
		return err
	}
	var owner string
	label := fmt.Sprintf("{{json (index .Config.Labels %s)}}", strconv.Quote(offlineDockerLabel))
	if err := d.inspect(ctx, &owner, "container", "inspect", "--format", label, id); err != nil {
		return err
	}
	if owner != key {
		return errors.New("refusing to remove an offline verifier container without matching ownership")
	}
	if _, err := d.read(ctx, "container", "rm", "--force", id); err != nil {
		return err
	}
	if remaining, err := d.find(ctx, key); err != nil || remaining != "" {
		return errors.Join(errors.New("offline verifier container removal could not be verified"), err)
	}
	return nil
}

func (d *OfflineDocker) find(ctx context.Context, key string) (string, error) {
	raw, err := d.read(ctx, "container", "ls", "--all", "--quiet", "--no-trunc", "--filter", "name=^/"+key+"$")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if id != "" && !dockerID.MatchString(id) {
		return "", errors.New("offline verifier container lookup was ambiguous")
	}
	return id, nil
}

func (d *OfflineDocker) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := process.Command(ctx, d.binary, append([]string{"--config", d.config, "--host", "unix://" + offlineDockerSocket}, args...)...)
	env := []string{"HOME=" + d.config, "PATH=" + filepath.Dir(d.binary) + ":/usr/bin:/bin"}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=") {
			env = append(env, entry)
		}
	}
	cmd.Env = env
	return cmd
}

func (d *OfflineDocker) inspect(ctx context.Context, destination any, args ...string) error {
	raw, err := d.read(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, destination)
}

func (d *OfflineDocker) read(ctx context.Context, args ...string) ([]byte, error) {
	cmd := d.command(ctx, args...)
	var out, diagnostics boundedDockerOutput
	cmd.Stdout, cmd.Stderr = &out, &diagnostics
	if err := process.Run(cmd); err != nil {
		return nil, fmt.Errorf("Docker %s command failed: %w: %s", args[0], err, strings.TrimSpace(diagnostics.String()[:min(2048, diagnostics.Len())]))
	}
	return out.Bytes(), nil
}

type boundedDockerOutput struct{ bytes.Buffer }

func (out *boundedDockerOutput) Write(p []byte) (int, error) {
	if out.Len()+len(p) > 1<<20 {
		return 0, errors.New("Docker metadata exceeds 1 MiB")
	}
	return out.Buffer.Write(p)
}

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type offlineACPAPI struct {
	client *http.Client
	daemon string
}

func openOfflineACPAPI(ctx context.Context) (*offlineACPAPI, error) {
	info, err := os.Stat(offlineDockerSocket)
	if os.Geteuid() == 0 || err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("offline ACP requires the managed rootless Docker socket and a non-root supervisor")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", offlineDockerSocket)
		},
		DisableKeepAlives: true, ResponseHeaderTimeout: 40 * time.Second,
	}
	api := &offlineACPAPI{client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	var status struct {
		ID              string
		SecurityOptions []string
	}
	if _, err := api.request(ctx, http.MethodGet, "/info", nil, &status, http.StatusOK); err != nil {
		return nil, err
	}
	if !validOfflineACPDaemon(status.ID) || !slices.Contains(status.SecurityOptions, "name=rootless") || !slices.Contains(status.SecurityOptions, "name=seccomp,profile=builtin") {
		return nil, errors.New("offline ACP requires rootless Docker with built-in seccomp")
	}
	api.daemon = status.ID
	return api, nil
}

func validOfflineACPDaemon(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != ':' && r != '-' {
			return false
		}
	}
	return true
}

func (api *offlineACPAPI) request(ctx context.Context, method, path string, payload, result any, allowed ...int) (int, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.43"+path, body)
	if err != nil {
		return 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := api.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if !slices.Contains(allowed, response.StatusCode) {
		return response.StatusCode, fmt.Errorf("offline ACP Docker %s returned HTTP %d", method, response.StatusCode)
	}
	if result != nil && response.StatusCode != http.StatusNotFound {
		return response.StatusCode, json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(result)
	}
	return response.StatusCode, nil
}

func (api *offlineACPAPI) image(ctx context.Context, ref string) (string, error) {
	var image struct {
		ID, Os      string
		RepoDigests []string
		Config      struct{ Volumes map[string]any }
	}
	_, err := api.request(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil, &image, http.StatusOK)
	if err != nil {
		return "", err
	}
	if !IsOfflineDockerImageID(image.ID) || image.Os != "linux" || !slices.Contains(image.RepoDigests, ref) || len(image.Config.Volumes) != 0 {
		return "", errors.New("offline ACP image must match the local pinned digest and declare no volumes")
	}
	return image.ID, nil
}

type offlineACPMount struct {
	Type, Source, Target string
	ReadOnly             bool
	BindOptions          struct {
		Propagation  string
		NonRecursive bool
	}
}

func offlineACPBind(source, target string) offlineACPMount {
	bind := offlineACPMount{Type: "bind", Source: source, Target: target, ReadOnly: true}
	bind.BindOptions.Propagation = "rprivate"
	bind.BindOptions.NonRecursive = true
	return bind
}

func offlineACPUser() string { return fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid()) }

func offlineACPConfig(r offlineACPRecord, p offlineACPProfile, mounts []offlineACPMount) map[string]any {
	return map[string]any{
		"Image": r.Image, "Entrypoint": []string{offlineACPProgram}, "Cmd": []string{},
		"WorkingDir": p.cwd, "User": offlineACPUser(), "Labels": map[string]string{offlineACPOwnerLabel: r.Token},
		"OpenStdin": true, "AttachStdin": true, "AttachStdout": true, "AttachStderr": true,
		"Healthcheck":     map[string]any{"Test": []string{"NONE"}},
		"Env":             []string{"HOME=/tmp", "TMPDIR=/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"},
		"NetworkDisabled": true,
		"HostConfig": map[string]any{
			"Runtime": "runc", "NetworkMode": "none", "IpcMode": "none", "ReadonlyRootfs": true,
			"CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges", "seccomp=builtin"},
			"Tmpfs": map[string]string{"/tmp": "rw,nosuid,nodev,size=64m"}, "Mounts": mounts,
			"Memory": 512 << 20, "NanoCpus": 1_000_000_000, "PidsLimit": 64,
			"LogConfig": map[string]string{"Type": "none"},
		},
	}
}

func (api *offlineACPAPI) start(ctx context.Context, root string, r offlineACPRecord, p offlineACPProfile) (_ *OfflineACPConnection, failure error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, removeOfflineACPRecord(root, r))
	}
	mounts := []offlineACPMount{offlineACPBind(p.cwd, p.cwd), offlineACPBind(p.program, offlineACPProgram)}
	var created struct{ ID string }
	defer func() {
		if failure != nil {
			cleanup, cancelCleanup := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancelCleanup()
			if _, err := api.reap(cleanup, root, r, false); err != nil {
				failure = errors.Join(failure, ErrIsolationUnverified, err)
			}
		}
	}()
	// A status request canceled by the caller does not cancel daemon-side
	// creation. Give it a separate bounded interval before recovery begins.
	creation, cancelCreation := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
	_, err := api.request(creation, http.MethodPost, "/containers/create?name="+url.QueryEscape(r.Name), offlineACPConfig(r, p, mounts), &created, http.StatusCreated)
	cancelCreation()
	if err != nil || !dockerID.MatchString(created.ID) {
		return nil, errors.Join(errors.New("offline ACP create outcome is unconfirmed"), err)
	}
	r.ID = created.ID
	if err := saveOfflineACPRecord(root, r); err != nil {
		return nil, err
	}
	setup, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	container, err := api.inspectOwned(setup, r)
	if err != nil {
		return nil, err
	}
	if err := container.verifyConfig(p, mounts); err != nil {
		return nil, err
	}
	stream, err := api.attach(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if _, err = api.request(setup, http.MethodPost, "/containers/"+r.ID+"/start", nil, nil, http.StatusNoContent); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &OfflineACPConnection{ReadWriteCloser: stream, docker: api, root: root, record: r}, nil
}

type offlineACPContainer struct {
	ID, Name, Image string
	Config          struct {
		Labels                          map[string]string
		User, WorkingDir                string
		Entrypoint, Cmd                 []string
		Tty, OpenStdin, NetworkDisabled bool
	}
	HostConfig struct {
		NetworkMode, IpcMode, Runtime, PidMode string
		Privileged, ReadonlyRootfs, AutoRemove bool
		CapAdd, CapDrop, SecurityOpt           []string
		Mounts                                 []offlineACPMount
		Tmpfs                                  map[string]string
		RestartPolicy                          struct{ Name string }
	}
	Mounts []struct {
		Type, Source, Destination string
		RW                        bool
	}
	State struct {
		Status, Error                        string
		Running, Restarting, OOMKilled, Dead bool
		Pid, ExitCode                        int
	}
}

func (c offlineACPContainer) verifyConfig(p offlineACPProfile, mounts []offlineACPMount) error {
	host := c.HostConfig
	if c.Config.User != offlineACPUser() || c.Config.WorkingDir != p.cwd || !slices.Equal(c.Config.Entrypoint, []string{offlineACPProgram}) ||
		len(c.Config.Cmd) != 0 || c.Config.Tty || !c.Config.OpenStdin || !c.Config.NetworkDisabled || host.Runtime != "runc" ||
		host.NetworkMode != "none" || host.IpcMode != "none" || host.PidMode != "" || host.Privileged || host.AutoRemove || !host.ReadonlyRootfs ||
		len(host.CapAdd) != 0 || !slices.Equal(host.CapDrop, []string{"ALL"}) || !slices.Contains(host.SecurityOpt, "no-new-privileges") ||
		!slices.Contains(host.SecurityOpt, "seccomp=builtin") || len(host.Tmpfs) != 1 || host.Tmpfs["/tmp"] != "rw,nosuid,nodev,size=64m" ||
		(host.RestartPolicy.Name != "" && host.RestartPolicy.Name != "no") {
		return errors.New("offline ACP runtime did not retain the confinement settings")
	}
	if !slices.Equal(host.Mounts, mounts) {
		return errors.New("offline ACP runtime did not retain the exact requested mounts")
	}
	var found int
	for _, m := range c.Mounts {
		if m.Type == "tmpfs" && m.Destination == "/tmp" {
			continue
		}
		if !slices.ContainsFunc(mounts, func(want offlineACPMount) bool {
			return m.Type == "bind" && m.Source == want.Source && m.Destination == want.Target && !m.RW
		}) {
			return errors.New("offline ACP runtime exposed an unexpected mount")
		}
		found++
	}
	if found != len(mounts) {
		return errors.New("offline ACP runtime lacks the exact read-only mounts")
	}
	for _, want := range mounts {
		if !slices.ContainsFunc(c.Mounts, func(m struct {
			Type, Source, Destination string
			RW                        bool
		}) bool {
			return m.Destination == want.Target
		}) {
			return errors.New("offline ACP runtime omitted a required mount")
		}
	}
	return nil
}

func (api *offlineACPAPI) inspectOwned(ctx context.Context, r offlineACPRecord) (offlineACPContainer, error) {
	var c offlineACPContainer
	id := r.ID
	if id == "" {
		id = r.Name
	}
	status, err := api.request(ctx, http.MethodGet, "/containers/"+id+"/json", nil, &c, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return c, err
	}
	if status == http.StatusNotFound {
		return c, os.ErrNotExist
	}
	if !dockerID.MatchString(c.ID) || (r.ID != "" && c.ID != r.ID) || c.Name != "/"+r.Name || c.Image != r.Image || c.Config.Labels[offlineACPOwnerLabel] != r.Token {
		return c, errors.New("offline ACP container identity or ownership mismatch")
	}
	return c, nil
}

func (api *offlineACPAPI) reap(ctx context.Context, root string, record offlineACPRecord, recovering bool) (int, error) {
	if !record.valid() || record.Daemon != api.daemon {
		return -1, errors.New("offline ACP recovery record does not match the authenticated Docker daemon")
	}
	c, err := api.inspectOwned(ctx, record)
	// A missing identity with a pending create cannot distinguish an orphan
	// that has not yet appeared from a failed create. Keep its record.
	if recovering && errors.Is(err, os.ErrNotExist) && record.ID != "" {
		if _, err := api.inspectOwned(ctx, offlineACPRecord{Name: record.Name, Token: record.Token, Image: record.Image}); !errors.Is(err, os.ErrNotExist) {
			return -1, errors.Join(errors.New("offline ACP name resolves after the recorded identity disappeared"), err)
		}
		return 0, removeOfflineACPRecord(root, record)
	}
	if err != nil {
		return -1, err
	}
	if c.State.Running {
		if _, err := api.request(ctx, http.MethodPost, "/containers/"+c.ID+"/stop?t=2", nil, nil, http.StatusNoContent, http.StatusNotModified); err != nil {
			return -1, err
		}
		record.ID = c.ID
		if c, err = api.inspectOwned(ctx, record); err != nil {
			return -1, err
		}
	}
	if c.State.Running || c.State.Restarting || c.State.Pid != 0 || (c.State.Status != "created" && c.State.Status != "exited") {
		return -1, errors.New("offline ACP runtime did not attest termination")
	}
	if _, err := api.request(ctx, http.MethodDelete, "/containers/"+c.ID, nil, nil, http.StatusNoContent); err != nil {
		return -1, err
	}
	record.ID = c.ID
	if _, err := api.inspectOwned(ctx, record); !errors.Is(err, os.ErrNotExist) {
		return -1, errors.Join(errors.New("offline ACP removal was not verified"), err)
	}
	if _, err := api.inspectOwned(ctx, offlineACPRecord{Name: record.Name, Token: record.Token, Image: record.Image}); !errors.Is(err, os.ErrNotExist) {
		return -1, errors.Join(errors.New("offline ACP name remains after removal"), err)
	}
	if err := removeOfflineACPRecord(root, record); err != nil {
		return -1, err
	}
	if c.State.Error != "" || c.State.OOMKilled || c.State.Dead {
		return c.State.ExitCode, errors.New("offline ACP runtime reported a failed container outcome")
	}
	return c.State.ExitCode, nil
}

func removeOfflineACPRecord(root string, record offlineACPRecord) error {
	if err := os.Remove(filepath.Join(root, record.Name+".json")); err != nil {
		return err
	}
	return syncOfflineACPRegistry(root)
}

func (api *offlineACPAPI) attach(ctx context.Context, id string) (io.ReadWriteCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/v1.43/containers/"+id+"/attach?stream=true&stdin=true&stdout=true&stderr=true", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	response, err := api.client.Do(req)
	if err != nil {
		return nil, err
	}
	stream, ok := response.Body.(io.ReadWriteCloser)
	if response.StatusCode != http.StatusSwitchingProtocols || !ok {
		_ = response.Body.Close()
		return nil, fmt.Errorf("offline ACP attachment requires raw Docker streaming, got HTTP %d", response.StatusCode)
	}
	return stream, nil
}

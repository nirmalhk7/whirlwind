package execution

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
	"whirlwind/internal/process"
	"whirlwind/internal/redaction"
)

const capabilityRegistrationError = "%w: %v"

type proxyCapabilities struct {
	releases []func()
	secrets  []string
}

func (c *proxyCapabilities) register(secret string) error {
	release, err := redaction.RegisterSecret(secret, 365*24*time.Hour)
	if err == nil {
		c.releases = append(c.releases, release)
		c.secrets = append(c.secrets, secret)
	}
	return err
}
func (c *proxyCapabilities) release() {
	for _, release := range c.releases {
		release()
	}
}
func (c *proxyCapabilities) retain() error {
	for _, secret := range c.secrets {
		if err := redaction.KeepSecretUntilReleased(secret); err != nil {
			return fmt.Errorf("cannot retain runtime credential redaction: %w", err)
		}
	}
	return nil
}
func (c *proxyCapabilities) environment(environment, names []string) error {
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		if !slices.Contains(names, name) || !secretEnvironmentName(name) || value == "" {
			continue
		}
		if len(value) < 16 {
			return fmt.Errorf("%w: credential environment reference %s is too short to register safely", ErrSandbox, name)
		}
		if err := c.register(value); err != nil {
			return err
		}
	}
	return nil
}
func registerProxyCapabilities(cmd *exec.Cmd, p Profile) (err error) {
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return fmt.Errorf("%w: cannot create per-sandbox proxy capability", ErrSandbox)
	}
	token := hex.EncodeToString(raw[:])
	capabilities := &proxyCapabilities{}
	defer func() {
		if err != nil {
			capabilities.release()
		}
	}()
	err = registerCommandCapabilities(cmd, p, token, capabilities)
	if err == nil {
		err = bindCommandCapabilities(cmd, p, token, capabilities)
	}
	return err
}
func registerCommandCapabilities(cmd *exec.Cmd, p Profile, token string, c *proxyCapabilities) error {
	secrets := []string{token, base64.StdEncoding.EncodeToString([]byte("whirlwind:" + token))}
	if p.MCPBroker != nil {
		secrets = append(secrets, p.MCPBrokerToken)
	}
	for _, secret := range secrets {
		if err := c.register(secret); err != nil {
			return fmt.Errorf(capabilityRegistrationError, ErrSandbox, err)
		}
	}
	return c.environment(cmd.Env, p.Environment)
}
func bindCommandCapabilities(cmd *exec.Cmd, p Profile, token string, c *proxyCapabilities) error {
	if err := process.OnCleanup(cmd, c.release); err != nil {
		return fmt.Errorf(capabilityRegistrationError, ErrSandbox, err)
	}
	if err := process.OnStart(cmd, c.retain); err != nil {
		return fmt.Errorf(capabilityRegistrationError, ErrSandbox, err)
	}
	proxyVariable := "WHIRLWIND_NONO_PROXY_TOKEN="
	if openShellLauncher(p.Launcher) {
		proxyVariable = "WHIRLWIND_OPENSHELL_PROXY_TOKEN="
	}
	cmd.Env = append(cmd.Env, proxyVariable+token)
	if p.MCPBroker != nil {
		cmd.Env = append(cmd.Env, "WHIRLWIND_MCP_BROKER_TOKEN="+p.MCPBrokerToken)
	}
	return nil
}

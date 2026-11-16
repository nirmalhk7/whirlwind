package execution

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func credentialReference(reference string) (scheme, name string, err error) {
	if !strings.Contains(reference, "://") {
		return "", "", nil
	}
	value, err := url.Parse(reference)
	if err != nil || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return "", "", errors.New("invalid credential source reference")
	}
	switch value.Scheme {
	case "env":
		if value.Path != "" || !safeCredentialEnvironment(value.Host) {
			return "", "", errors.New("invalid environment credential source")
		}
		return "env", value.Host, nil
	case "file":
		if value.Host != "" || !filepath.IsAbs(value.Path) {
			return "", "", errors.New("file credential source must be an absolute local path")
		}
		return "file", value.Path, nil
	case "keyring", "op", "apple-password":
		return value.Scheme, "", nil
	default:
		return "", "", errors.New("unsupported credential source reference")
	}
}

func safeCredentialEnvironment(name string) bool {
	return envName.MatchString(name) && !unsafeEnvironment(name) && !slices.Contains([]string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "SSL_CERT_FILE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "NODE_USE_ENV_PROXY"}, name)
}

func validateCredentialGrants(profile Profile) error {
	targets := map[string]bool{}
	for service, credential := range profile.Credentials {
		if targets[credential.EnvVar] {
			return errors.New("credential services cannot share a phantom environment variable")
		}
		targets[credential.EnvVar] = true
		if !safeCredentialEnvironment(credential.EnvVar) || credential.EnvVar == strings.ToUpper(service)+"_BASE_URL" {
			return errors.New("credential phantom environment cannot replace runtime bootstrap")
		}
		scheme, name, err := credentialReference(credential.CredentialKey)
		if err != nil {
			return err
		}
		if scheme != "file" {
			continue
		}
		if err := validateCredentialFileGrant(profile, name); err != nil {
			return err
		}
	}
	return nil
}

// References explicitly authorize host-side resolution. These values enter
// only the native supervisor environment, never profile JSON or agent input.
func credentialSupervisorEnvironment(environment []string, profile Profile) ([]string, error) {
	seen := map[string]bool{}
	for _, credential := range profile.Credentials {
		scheme, name, err := credentialReference(credential.CredentialKey)
		if err != nil {
			return nil, err
		}
		if scheme != "env" || seen[name] {
			continue
		}
		seen[name] = true
		value, exists := os.LookupEnv(name)
		if !exists || value == "" {
			return nil, fmt.Errorf("credential source environment %s is unavailable", name)
		}
		environment = slices.DeleteFunc(environment, func(entry string) bool { return strings.HasPrefix(entry, name+"=") })
		environment = append(environment, name+"="+value)
	}
	return environment, nil
}

func validateCredentialFileGrant(profile Profile, name string) error {
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return fmt.Errorf("credential source file unavailable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 1<<20 {
		return errors.New("credential source must be a bounded nonempty regular file")
	}
	roots := credentialFilesystemRoots(profile)
	for _, root := range roots {
		if root == "" {
			continue
		}
		if canonical, err := filepath.EvalSymlinks(root); err == nil {
			root = canonical
		}
		if pathInside(root, resolved) {
			return errors.New("proxy credential source must remain outside child filesystem grants")
		}
	}
	return nil
}

func credentialFilesystemRoots(profile Profile) []string {
	roots := append([]string{profile.CWD, profile.RuntimeDir}, profile.ReadRoots...)
	// The native launcher also grants these public runtime paths. A proxy
	// credential must not become readable through an implicit system grant.
	roots = append(roots, "/bin", "/sbin", "/usr", "/lib", "/lib64", "/System", "/Library/Developer/CommandLineTools", "/opt/homebrew/Cellar", "/opt/homebrew/opt", "/usr/local/Cellar", "/usr/local/opt", "/private/etc/ssl", "/etc/ssl/certs", "/etc/ld.so.cache", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group", "/etc/protocols", "/etc/services", "/etc/localtime", "/private/var/db/dyld", "/private/var/select", "/dev")
	roots = append(roots, profile.WriteRoots...)
	for _, file := range profile.NativeFiles {
		roots = append(roots, file.Source)
	}
	if profile.Launcher != "" {
		roots = append(roots, filepath.Dir(profile.Launcher))
	}
	return roots
}

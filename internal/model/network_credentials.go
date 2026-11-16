package model

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type NetworkEndpointRule struct {
	Method string `json:"method" yaml:"method"`
	Path   string `json:"path" yaml:"path"`
}

// Credential values are resolved by the native supervisor outside the child.
// Configuration stores references only, plus explicitly approved endpoint rules.
type NetworkCredential struct {
	Upstream         string                `json:"upstream" yaml:"upstream"`
	CredentialKey    string                `json:"credential_key" yaml:"credential_key"`
	EnvVar           string                `json:"env_var" yaml:"env_var"`
	InjectHeader     string                `json:"inject_header" yaml:"inject_header"`
	CredentialFormat string                `json:"credential_format" yaml:"credential_format"`
	EndpointRules    []NetworkEndpointRule `json:"endpoint_rules" yaml:"endpoint_rules"`
}

var networkServiceName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var networkCredentialHeader = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func ValidateNetworkCredential(service string, credential NetworkCredential) error {
	if !networkServiceName.MatchString(service) {
		return errors.New("invalid credential service identity")
	}
	upstream, err := url.Parse(credential.Upstream)
	if err != nil || upstream.Host == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" {
		return errors.New("credential upstream must name an explicit origin without URL credentials")
	}
	loopback := slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, upstream.Hostname())
	if upstream.Scheme != "https" && (upstream.Scheme != "http" || !loopback) {
		return errors.New("credential upstream requires HTTPS except explicit local loopback services")
	}
	if credential.CredentialKey == "" || len(credential.CredentialKey) > 4096 || strings.ContainsAny(credential.CredentialKey, "\x00\r\n") {
		return errors.New("credential source reference required")
	}
	if !validCredentialInjection(credential) {
		return errors.New("credential route requires explicit safe environment, header and format")
	}
	if len(credential.EndpointRules) == 0 || len(credential.EndpointRules) > 128 {
		return errors.New("credential route requires explicit endpoint rules")
	}
	for _, rule := range credential.EndpointRules {
		if !slices.Contains([]string{"*", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, rule.Method) || !strings.HasPrefix(rule.Path, "/") || strings.ContainsAny(rule.Path, "?#\\\x00\r\n ") {
			return errors.New("invalid credential endpoint method or path")
		}
	}
	return nil
}

func validCredentialInjection(credential NetworkCredential) bool {
	return credential.EnvVar != "" && networkCredentialHeader.MatchString(credential.InjectHeader) && !slices.Contains([]string{"host", "connection", "proxy-authorization"}, strings.ToLower(credential.InjectHeader)) && strings.Count(credential.CredentialFormat, "{}") == 1 && !strings.ContainsAny(credential.CredentialFormat, "\x00\r\n")
}

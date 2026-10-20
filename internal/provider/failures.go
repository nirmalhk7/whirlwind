package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"syscall"
)

// FailureKind distinguishes retryable transport failures from blocked configurations.
type FailureKind string

const (
	FailureUnknown       FailureKind = "unknown"
	FailurePolicy        FailureKind = "policy"
	FailureTransient     FailureKind = "transient"
	FailureRateLimit     FailureKind = "rate_limit"
	FailureAuth          FailureKind = "authentication"
	FailureQuota         FailureKind = "quota"
	FailureConfiguration FailureKind = "configuration"
)

// RPCError retains the provider's structured error without losing its diagnostic data.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("provider RPC (%d): %s %s", e.Code, e.Message, e.Data)
}

func (e *RPCError) RPCCode() int { return e.Code }

// TransportError preserves a failed process or connection's underlying cause.
type TransportError struct{ Cause error }

func (e *TransportError) Error() string { return "provider transport: " + e.Cause.Error() }
func (e *TransportError) Unwrap() error { return e.Cause }

// ClassifyFailure only inspects returned execution errors, never assistant output.
func ClassifyFailure(err error) FailureKind {
	if err == nil || callerCancellation(err) {
		return FailureUnknown
	}
	if errors.Is(err, exec.ErrNotFound) {
		return FailureConfiguration
	}
	if kind := remoteFailure(err); kind != FailureUnknown {
		return kind
	}
	var transport *TransportError
	var network net.Error
	var exited *exec.ExitError
	if errors.As(err, &transport) || errors.As(err, &network) || errors.As(err, &exited) || brokenConnection(err) {
		return FailureTransient
	}
	return FailureUnknown
}

func brokenConnection(err error) bool {
	for _, cause := range []error{io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, syscall.EPIPE, syscall.ECONNRESET, syscall.ECONNREFUSED} {
		if errors.Is(err, cause) {
			return true
		}
	}
	return false
}

func rpcFailure(code int) FailureKind {
	switch code {
	case 401, 403:
		return FailureAuth
	case 429:
		return FailureRateLimit
	case 408, 500, 502, 503, 504:
		return FailureTransient
	case -32601, -32602:
		return FailureConfiguration
	default:
		return FailureUnknown
	}
}

func namedFailure(message string) FailureKind {
	message = strings.ToLower(message)
	groups := []struct {
		kind    FailureKind
		markers []string
	}{
		{FailurePolicy, []string{"tool permission denied", "read-only agent role", "path escapes worktree", "direct git metadata access denied", "cyberpolicy", "misalignmentpolicyviolation", "toomanydenials", "sandboxerror"}},
		{FailureAuth, []string{"authentication", "not logged in", "sign in", "login required", "unauthorized", "disabled claude subscription access"}},
		{FailureQuota, []string{"quota exhausted", "quota exceeded", "insufficient_quota", "usage_limit", "usagelimitreached", "usagelimitexceeded", "weekly limit reached"}},
		{FailureConfiguration, []string{"invalid model", "model not found", "unsupported", "not advertised", "not available in provider"}},
		{FailureRateLimit, []string{"rate limit", "rate_limit", "ratelimitexceeded", "too many requests"}},
		{FailureTransient, []string{"service unavailable", "temporarily unavailable", "server overloaded", "serveroverloaded", "internalservererror", "flexunavailable", "httpconnectionfailed", "responsestreamconnectionfailed", "responsestreamdisconnected", "responsetoomanyfailedattempts"}},
	}
	for _, group := range groups {
		for _, marker := range group.markers {
			if strings.Contains(message, marker) {
				return group.kind
			}
		}
	}
	return FailureUnknown
}

func remoteFailure(err error) FailureKind {
	kind := namedFailure(err.Error())
	if kind == FailureAuth || kind == FailureQuota || kind == FailurePolicy {
		return kind
	}
	var rpc *RPCError
	if errors.As(err, &rpc) {
		if coded := rpcFailure(rpc.Code); coded != FailureUnknown {
			return coded
		}
	}
	return kind
}

func callerCancellation(err error) bool {
	var transport *TransportError
	return !errors.As(err, &transport) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

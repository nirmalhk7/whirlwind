package provider

import (
	"errors"
	"strings"
)

func (s *providerSession) openNativeThread(method string, params map[string]any, result any) error {
	err := s.rpc.Call(s.ctx, method, params, result)
	if params["dynamicTools"] != nil && unsupportedMemory(err) {
		delete(params, "dynamicTools")
		s.runtimeEvent("whirlwind/capability_warning", "On-demand memory unavailable in this Codex version; automatic recall remains active")
		err = s.rpc.Call(s.ctx, method, params, result)
	}
	return err
}

func unsupportedMemory(err error) bool {
	var failure *RPCError
	if !errors.As(err, &failure) || failure.Code != -32602 {
		return false
	}
	message := strings.ToLower(failure.Message)
	return strings.Contains(message, "dynamictools") || strings.Contains(message, "experimentalapi")
}

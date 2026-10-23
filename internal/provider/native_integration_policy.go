package provider

import (
	"slices"
	"strconv"
	"strings"

	"whirlwind/internal/model"
)

const (
	nativePolicyTools           = ".tools."
	nativePolicyApproval        = ".approval_mode"
	nativePolicyDefaultApproval = ".default_tools_approval_mode"
	nativePolicyEnabled         = ".enabled"
	nativePolicyReviewer        = ".approvals_reviewer"
	nativePolicyDestructive     = ".destructive_enabled"
	nativePolicyPrompt          = "prompt"
	nativePolicyAuto            = "auto"
	nativePolicyAppsDefault     = "apps._default"
)

func nativeConfigKey(parts ...string) string {
	for i, part := range parts {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool {
			return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", r)
		}) {
			parts[i] = strconv.Quote(part)
		}
	}
	return strings.Join(parts, ".")
}

// nativeIntegrationPolicy applies only session-local restrictions. It does not
// enable connections or tools which native runtime policy disabled.
func (s *providerSession) nativeIntegrationPolicy(catalog []model.Integration) map[string]any {
	config := map[string]any{nativePolicyAppsDefault + nativePolicyDefaultApproval: nativePolicyPrompt, nativePolicyAppsDefault + nativePolicyReviewer: "user"}
	if s.input.ReadOnly {
		config[nativePolicyAppsDefault+nativePolicyEnabled] = false
		config[nativePolicyAppsDefault+nativePolicyDestructive] = false
	}
	for _, item := range catalog {
		switch item.Kind {
		case "app":
			s.applyNativeAppPolicy(config, item)
		case "mcp":
			s.applyNativeMCPPolicy(config, item)
		}
	}
	return config
}

func (s *providerSession) applyNativeAppPolicy(config map[string]any, item model.Integration) {
	prefix := nativeConfigKey("apps", strings.TrimPrefix(item.Ref, "app://"))
	if s.input.ReadOnly {
		config[prefix+nativePolicyEnabled] = item.Enabled && item.Callable
		config[prefix+".default_tools_enabled"] = false
		config[prefix+nativePolicyDestructive] = false
	}
	config[prefix+nativePolicyDefaultApproval] = nativePolicyPrompt
	config[prefix+nativePolicyReviewer] = "user"
	for _, tool := range item.Tools {
		s.applyNativeAppToolPolicy(config, prefix, item, tool)
	}
}

func (s *providerSession) applyNativeAppToolPolicy(config map[string]any, prefix string, item model.Integration, tool model.IntegrationTool) {
	key := prefix + nativePolicyTools + nativeConfigKey(tool.Name)
	if s.input.ReadOnly {
		config[key+nativePolicyEnabled] = item.Enabled && item.Callable && tool.ReadOnly
	}
	config[key+nativePolicyApproval] = s.nativeToolApproval(tool)
}

func (s *providerSession) applyNativeMCPPolicy(config map[string]any, item model.Integration) {
	prefix := s.nativePolicies[item.Ref]
	if prefix == "" {
		prefix = nativeConfigKey("mcp_servers", strings.TrimPrefix(item.Ref, "mcp://"))
	}
	safe := []string{}
	for _, tool := range item.Tools {
		if item.Enabled && item.Callable && tool.ReadOnly {
			safe = append(safe, tool.Name)
		}
		key := prefix + nativePolicyTools + nativeConfigKey(tool.Name)
		config[key+nativePolicyApproval] = s.nativeToolApproval(tool)
	}
	if s.input.ReadOnly {
		slices.Sort(safe)
		config[prefix+".enabled_tools"] = safe
	}
	config[prefix+nativePolicyDefaultApproval] = nativePolicyPrompt
}

func (s *providerSession) nativeToolApproval(tool model.IntegrationTool) string {
	if s.input.ReadOnly && tool.ReadOnly {
		return nativePolicyAuto
	}
	return nativePolicyPrompt
}

package execution

import (
	"fmt"
	"reflect"
	"slices"

	"whirlwind/internal/model"
)

// Both inputs are human configuration. Provider transport exceptions do not
// change worktree write authority, which remains derived from the active role.
func CombineSandboxPolicies(project, provider *model.SandboxPolicy) (*model.SandboxPolicy, error) {
	if project == nil && provider == nil {
		return nil, nil
	}
	combined := &model.SandboxPolicy{Credentials: map[string]model.NetworkCredential{}}
	for _, policy := range []*model.SandboxPolicy{project, provider} {
		if policy == nil {
			continue
		}
		combined.ReadRoots = append(combined.ReadRoots, policy.ReadRoots...)
		combined.AllowedDomains = append(combined.AllowedDomains, policy.AllowedDomains...)
		combined.Environment = append(combined.Environment, policy.Environment...)
		for service, credential := range policy.Credentials {
			if existing, ok := combined.Credentials[service]; ok && !reflect.DeepEqual(existing, credential) {
				return nil, fmt.Errorf("conflicting project/provider credential binding for %s", service)
			}
			credential.EndpointRules = slices.Clone(credential.EndpointRules)
			combined.Credentials[service] = credential
		}
	}
	for _, values := range []*[]string{&combined.ReadRoots, &combined.AllowedDomains, &combined.Environment} {
		slices.Sort(*values)
		*values = slices.Compact(*values)
	}
	if len(combined.Credentials) == 0 {
		combined.Credentials = nil
	}
	return combined, nil
}

package model

// SandboxPolicy grants only explicit read, network, and environment exceptions.
// Writable authority is derived from the role and its isolated worktree.
type SandboxPolicy struct {
	ReadRoots      []string `json:"read_roots,omitempty" yaml:"read_roots"`
	AllowedDomains []string `json:"allowed_domains,omitempty" yaml:"allowed_domains"`
	Environment    []string `json:"environment,omitempty" yaml:"environment"`
}

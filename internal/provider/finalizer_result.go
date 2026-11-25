package provider

// FinalizationResult is returned by the trusted supervisor operation. Agents
// supply no Git arguments, repository paths, identities or branch names.
type FinalizationResult struct {
	Pending bool   `json:"pending,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Tree    string `json:"tree,omitempty"`
}

package provider

// FinalizationResult is returned by the trusted supervisor operation. Agents
// supply no Git arguments, repository paths, identities or branch names.
type FinalizationResult struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
}

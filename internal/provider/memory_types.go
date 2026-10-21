package provider

// MemoryConnection is ephemeral; credentials must not enter attempt snapshots.
type MemoryConnection struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	Command string `json:"command"`
}

package model

// DispatchHealth describes the last sandbox recovery check in this
// supervisor process. An online supervisor can still be unable to dispatch.
type DispatchHealth struct {
	Checked     bool   `json:"checked"`
	BlockReason string `json:"block_reason,omitempty"`
}

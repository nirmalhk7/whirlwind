package provider

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

// RetryAfter accepts structured retry delays; assistant text never controls scheduling.
func RetryAfter(err error) time.Duration {
	var rpc *RPCError
	if !errors.As(err, &rpc) {
		return 0
	}
	var data struct {
		RetryAfter        *float64 `json:"retryAfter"`
		RetryAfterSeconds *float64 `json:"retry_after_seconds"`
	}
	if json.Unmarshal(rpc.Data, &data) != nil {
		return 0
	}
	n := data.RetryAfterSeconds
	if n == nil {
		n = data.RetryAfter
	}
	if n == nil || math.IsNaN(*n) || math.IsInf(*n, 0) || *n <= 0 {
		return 0
	}
	// Preserve the reported delay while keeping duration conversion finite.
	maxSeconds := float64(int64(math.MaxInt64)/int64(time.Second) - 1)
	seconds := min(*n, maxSeconds)
	return time.Duration(seconds * float64(time.Second))
}

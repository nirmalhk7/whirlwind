// Package rolequeue provides the Redis transport for Whirlwind's role handoffs.
// The supervisor reconciles it from committed SQLite state; Redis is not an
// authorization source and contains no plans, prompts, diffs, or review text.
package rolequeue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Role string

const (
	Planner   Role = "planner_tasks"
	Coding    Role = "coding_tasks"
	Reviewer  Role = "reviewer_tasks"
	Finalizer Role = "finalizer_tasks"
)

func Roles() [4]Role { return [...]Role{Planner, Coding, Reviewer, Finalizer} }

// Delivery references one immutable SQLite resource revision. Stage and
// UpdatedAt protect against stale deliveries, including delete/recreate.
type Delivery struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	Stage     string `json:"stage"`
	UpdatedAt string `json:"updated_at"`
}

// Transport reconciles each role's eligible deliveries and returns its oldest
// delivery. Committed local state acknowledges a delivery on a subsequent sync.
type Transport interface {
	Head(context.Context, Role, []Delivery) (Delivery, bool, error)
}

type Redis struct {
	client    *redis.Client
	namespace string
}

// Open configures a standalone Redis client. It never includes the connection
// URL or server text in returned errors because they may contain credentials.
func Open(rawURL, namespace string) (*Redis, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid WHIRLWIND_REDIS_URL; use redis:// or rediss://")
	}
	if !strings.HasPrefix(rawURL, "redis://") && !strings.HasPrefix(rawURL, "rediss://") {
		return nil, errors.New("WHIRLWIND_REDIS_URL must use redis:// or rediss://")
	}
	if namespace == "" || len(namespace) > 128 || strings.ContainsAny(namespace, " \t\r\n\x00") {
		return nil, errors.New("WHIRLWIND_REDIS_NAMESPACE must be a nonempty, whitespace-free name of up to 128 bytes")
	}
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = 2*time.Second, 2*time.Second, 2*time.Second
	opt.ContextTimeoutEnabled = true
	opt.MaxRetries = -1
	return &Redis{client: redis.NewClient(opt), namespace: namespace}, nil
}

func (q *Redis) Close() error { return q.client.Close() }

// One atomic Redis operation deletes stale revisions and appends new ones.
// Existing score/order survives duplicate reconciliation or a lost reply.
// A single supervisor owns the namespace for one SQLite state directory.
var syncHead = redis.NewScript(`
local key = KEYS[1]
local eligible = {}
for _, member in ipairs(ARGV) do eligible[member] = true end
for _, member in ipairs(redis.call('ZRANGE', key, 0, -1)) do
    if not eligible[member] then redis.call('ZREM', key, member) end
end
local last = redis.call('ZREVRANGE', key, 0, 0, 'WITHSCORES')
local seq = tonumber(last[2]) or 0
for _, member in ipairs(ARGV) do
    if redis.call('ZADD', key, 'NX', seq+1, member) == 1 then seq = seq+1 end
end
return redis.call('ZRANGE', key, 0, 0)
`)

func (q *Redis) Head(ctx context.Context, role Role, candidates []Delivery) (Delivery, bool, error) {
	switch role {
	case Planner, Coding, Reviewer, Finalizer:
	default:
		return Delivery{}, false, errors.New("unknown Redis task queue")
	}
	args := make([]any, 0, len(candidates))
	for _, delivery := range candidates {
		data, err := json.Marshal(delivery)
		if err != nil {
			return Delivery{}, false, err
		}
		args = append(args, string(data))
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	reply, err := syncHead.Run(ctx, q.client, []string{q.namespace + ":" + string(role)}, args...).StringSlice()
	if err != nil {
		if ctx.Err() != nil {
			return Delivery{}, false, ctx.Err()
		}
		return Delivery{}, false, fmt.Errorf("Redis queue %s unavailable", role)
	}
	if len(reply) == 0 {
		return Delivery{}, false, nil
	}
	var delivery Delivery
	if len(reply) != 1 || json.Unmarshal([]byte(reply[0]), &delivery) != nil {
		return Delivery{}, false, fmt.Errorf("invalid Redis delivery from %s", role)
	}
	return delivery, true, nil
}

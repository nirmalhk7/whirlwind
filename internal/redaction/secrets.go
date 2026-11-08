package redaction

import (
	"errors"
	"strings"
	"sync"
	"time"
)

const maxRegisteredSecrets = 8192

var ErrInvalidSecret = errors.New("redaction secret must contain at least 16 characters")
var ErrRegistryFull = errors.New("runtime redaction registry is full")

type registration struct {
	expires time.Time
	refs    int
}

var secrets = struct {
	sync.RWMutex
	values map[string]registration
}{values: make(map[string]registration)}

// RegisterSecret keeps runtime credentials redacted until their owner releases them.
// TTL provides bounded cleanup if the process never reaches its cleanup hook.
func RegisterSecret(secret string, ttl time.Duration) (func(), error) {
	if len(secret) < 16 {
		return nil, ErrInvalidSecret
	}
	if ttl <= 0 {
		ttl = 365 * 24 * time.Hour
	}
	secrets.Lock()
	err := registerLocked(secret, ttl, time.Now())
	secrets.Unlock()
	if err != nil {
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			secrets.Lock()
			entry, ok := secrets.values[secret]
			if ok {
				entry.refs--
				if entry.refs <= 0 {
					delete(secrets.values, secret)
				} else {
					secrets.values[secret] = entry
				}
			}
			secrets.Unlock()
		})
	}, nil
}

func registerLocked(secret string, ttl time.Duration, now time.Time) error {
	pruneExpiredLocked(now)
	entry, exists := secrets.values[secret]
	if !exists && len(secrets.values) >= maxRegisteredSecrets {
		return ErrRegistryFull
	}
	entry.refs++
	entry.expires = extendedExpiry(entry, exists, now.Add(ttl))
	secrets.values[secret] = entry
	return nil
}

func pruneExpiredLocked(now time.Time) {
	for value, entry := range secrets.values {
		if !entry.expires.IsZero() && !entry.expires.After(now) {
			delete(secrets.values, value)
		}
	}
}

func extendedExpiry(entry registration, exists bool, expiry time.Time) time.Time {
	if exists && entry.expires.IsZero() {
		return time.Time{}
	}
	if !exists || expiry.After(entry.expires) {
		return expiry
	}
	return entry.expires
}

// KeepSecretUntilReleased promotes a pending command's redaction registration
// to the active-process lifetime after successful process start.
func KeepSecretUntilReleased(secret string) error {
	secrets.Lock()
	defer secrets.Unlock()
	entry, ok := secrets.values[secret]
	if !ok || entry.refs <= 0 || !entry.expires.After(time.Now()) {
		return errors.New("redaction registration unavailable for active process")
	}
	entry.expires = time.Time{}
	secrets.values[secret] = entry
	return nil
}

// RedactText replaces active runtime secrets in an otherwise useful string.
func RedactText(text string) string {
	secrets.RLock()
	values := make([]string, 0, len(secrets.values))
	now := time.Now()
	for secret, entry := range secrets.values {
		if entry.expires.IsZero() || entry.expires.After(now) {
			values = append(values, secret)
		}
	}
	secrets.RUnlock()
	for _, secret := range values {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	return text
}

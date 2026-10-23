package workflow

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentRun) cooldown(p model.Provider) error {
	pool := providerPool(p)
	record, err := a.Store.Get(a.ctx, "provider_cooldowns", pool)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	value, err := store.Decode[model.ProviderCooldown](record)
	if err != nil {
		return err
	}
	until, err := time.Parse(time.RFC3339Nano, value.Until)
	if err != nil {
		return err
	}
	if time.Now().Before(until) {
		return &AdmissionWait{Kind: "retry", Reason: value.Reason, Pool: pool, Provider: p.ID, Role: a.input.Role, NextCheck: until}
	}
	return nil
}
func (a *agentAttempt) saveCooldown(delay time.Duration, reason string) (*AdmissionWait, error) {
	pool := providerPool(a.chosen)
	record, err := a.Store.Get(a.ctx, "provider_cooldowns", pool)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	until := time.Now().Add(delay)
	if err == nil {
		old, decodeErr := store.Decode[model.ProviderCooldown](record)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if oldUntil, parseErr := time.Parse(time.RFC3339Nano, old.Until); parseErr == nil && oldUntil.After(until) {
			until = oldUntil
		}
	}
	value := model.ProviderCooldown{Pool: pool, AttemptID: a.id, Until: until.UTC().Format(time.RFC3339Nano), Reason: reason}
	if err = a.Store.Put(a.ctx, "provider_cooldowns", pool, value, record.Version); err != nil {
		return nil, err
	}
	return &AdmissionWait{Kind: "retry", Reason: reason, Pool: pool, Provider: a.chosen.ID, Role: a.input.Role, NextCheck: until}, nil
}
func (a *agentAttempt) clearOwnCooldown() error {
	pool := providerPool(a.chosen)
	record, err := a.Store.Get(a.ctx, "provider_cooldowns", pool)
	if err != nil {
		return err
	}
	value, err := store.Decode[model.ProviderCooldown](record)
	if err != nil {
		return err
	}
	if value.AttemptID != a.id {
		return nil
	}
	value.Until = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	return a.Store.Put(a.ctx, "provider_cooldowns", pool, value, record.Version)
}
func retryJitter(delay time.Duration) time.Duration {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return delay
	}
	window := int64(delay / 5)
	if window <= 0 {
		return delay
	}
	return delay + time.Duration(binary.LittleEndian.Uint64(b[:])%uint64(window))
}

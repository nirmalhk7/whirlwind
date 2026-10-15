package workflow

import (
	"context"
	"errors"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentAttempt) retry(err error) bool {
	if err == nil || errors.Is(err, store.ErrPersistence) {
		return false
	}
	latest, version, readErr := a.readRetryProvider()
	if readErr != nil {
		_ = a.persistenceError(readErr)
		return false
	}
	quota, auth := providerFailure(err, latest.Quota)
	if err == nil || (!quota && !auth) {
		return false
	}
	a.fallbackReason = err.Error()
	a.chosen = latest
	if auth {
		a.chosen.Available = false
		a.chosen.Limitation = "Authentication required; use provider login and recheck setup"
	} else {
		a.chosen.Quota = "exhausted"
	}
	if putErr := a.Store.Put(context.Background(), "providers", a.chosen.ID, a.chosen, version); putErr != nil {
		_ = a.persistenceError(putErr)
		return false
	}
	for n := range a.providers {
		if a.providers[n].ID == a.chosen.ID {
			a.providers[n] = a.chosen
			a.providers[n].Available = false
		}
	}
	a.input.SessionID = ""
	a.freshSession = true
	a.input.Prompt += "\nPrevious provider unavailable. Continue existing worktree from handoff; preserve completed work.\n" + jsonText(a.task.Handoff)
	return true
}

func (a *agentAttempt) readRetryProvider() (model.Provider, int, error) {
	record, err := a.Store.Get(context.Background(), "providers", a.chosen.ID)
	if err != nil {
		return model.Provider{}, 0, err
	}
	latest, err := store.Decode[model.Provider](record)
	return latest, record.Version, err
}

func providerFailure(err error, quotaState string) (bool, bool) {
	quota := quotaState == "exhausted"
	auth := false
	if err != nil {
		message := strings.ToLower(err.Error())
		for _, marker := range []string{"quota", "rate limit", "rate_limit", "ratelimitexceeded", "usage_limit", "usagelimitreached", "usagelimitexceeded"} {
			quota = quota || strings.Contains(message, marker)
		}
		for _, marker := range []string{"authentication", "not logged in", "sign in", "login required", "unauthorized"} {
			auth = auth || strings.Contains(message, marker)
		}
	}
	return quota, auth
}

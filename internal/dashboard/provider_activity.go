package dashboard

import (
	"time"

	"whirlwind/internal/model"
)

// Progress measures recorded ACP updates, not worker health or a prediction
// about whether a provider request will eventually finish.
func providerActivity(attempt model.Attempt, now time.Time) string {
	if attempt.ProviderProgressAt == "" {
		return "unknown (not recorded)"
	}
	progress, err := time.Parse(time.RFC3339Nano, attempt.ProviderProgressAt)
	if err != nil || progress.After(now) {
		return "unknown (invalid update timestamp)"
	}
	timestamp := progress.UTC().Format(time.RFC3339Nano)
	if attempt.StartedAt == "" {
		return "recorded at " + timestamp + " (launch time not recorded)"
	}
	start, err := time.Parse(time.RFC3339Nano, attempt.StartedAt)
	if err != nil || progress.Before(start) {
		return "unknown (update cannot be reconciled with launch)"
	}
	if attempt.FinishedAt != "" {
		finish, err := time.Parse(time.RFC3339Nano, attempt.FinishedAt)
		if err != nil || progress.After(finish) {
			return "unknown (update cannot be reconciled with finish)"
		}
	}
	label := "last update at "
	if progress.Equal(start) {
		label = "no update since launch at "
	}
	if activeAttempt(attempt) {
		return label + timestamp + " (" + now.Sub(progress).Truncate(time.Second).String() + " ago)"
	}
	return label + timestamp
}

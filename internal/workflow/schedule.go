package workflow

import (
	"context"
	"fmt"
	"time"
	_ "time/tzdata"
)

type IdeationSchedule struct {
	MaxCandidates int    `json:"max_candidates,omitempty"`
	Enabled       bool   `json:"enabled"`
	Time          string `json:"time"`
	Timezone      string `json:"timezone"`
}

func DefaultSettings() Settings {
	return Settings{GlobalSlots: 4, ProjectSlots: 2, ReconciliationSeconds: 300, ProviderStatusCheckSeconds: 30, Ideation: IdeationSchedule{Enabled: true, Time: "10:00", Timezone: "America/Denver"}}
}

func (s IdeationSchedule) Validate() error {
	if s.MaxCandidates < 0 || s.MaxCandidates > 12 {
		return fmt.Errorf("max_candidates must be between 1 and 12 when configured")
	}
	if _, err := time.Parse("15:04", s.Time); err != nil {
		return fmt.Errorf("ideation time must be HH:MM")
	}
	if s.Timezone == "" || s.Timezone == "Local" {
		return fmt.Errorf("ideation timezone must be an explicit IANA timezone")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid ideation timezone: %s", s.Timezone)
	}
	return nil
}

// DueDay selects the latest scheduled local date; restarts catch up only that date.
// Comparing wall-clock minutes runs once even across repeated DST hours.
func (s IdeationSchedule) DueDay(now time.Time) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	location, _ := time.LoadLocation(s.Timezone)
	clock, _ := time.Parse("15:04", s.Time)
	local := now.In(location)
	if local.Hour()*60+local.Minute() < clock.Hour()*60+clock.Minute() {
		// Use noon so AddDate remains stable across DST transitions at midnight.
		local = time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, location).AddDate(0, 0, -1)
	}
	return local.Format("2006-01-02"), nil
}

func (e *Engine) IdeationDay(ctx context.Context, now time.Time) (string, error) {
	return e.settings(ctx).Ideation.DueDay(now)
}

package workflow

import (
	"context"
	"fmt"
	"time"
	_ "time/tzdata"
)

type IdeationSchedule struct {
	MaxCandidates         int    `json:"max_candidates,omitempty"`
	HistoryTokenBudget    int    `json:"history_token_budget,omitempty"`
	HistoryRunTokenBudget int    `json:"history_run_token_budget,omitempty"`
	HistoryRecordLimit    int    `json:"history_record_limit,omitempty"`
	NoveltyRepairAttempts int    `json:"novelty_repair_attempts,omitempty"`
	Enabled               bool   `json:"enabled"`
	Time                  string `json:"time"`
	Timezone              string `json:"timezone"`
}

func DefaultSettings() Settings {
	return Settings{GlobalSlots: 4, ProjectSlots: 2, ReconciliationSeconds: 300, ProviderStatusCheckSeconds: 30, Ideation: IdeationSchedule{Enabled: true, Time: "10:00", Timezone: "America/Denver", HistoryTokenBudget: 4000, HistoryRunTokenBudget: 12000, HistoryRecordLimit: 12, NoveltyRepairAttempts: 3}}
}

func (s IdeationSchedule) Validate() error {
	if s.MaxCandidates < 0 || s.MaxCandidates > 12 {
		return fmt.Errorf("max_candidates must be between 1 and 12 when configured")
	}
	if s.HistoryTokenBudget < 0 || s.HistoryTokenBudget > 4000 || s.HistoryRunTokenBudget < 0 || s.HistoryRecordLimit < 0 || s.HistoryRecordLimit > 12 || s.NoveltyRepairAttempts < 0 || s.NoveltyRepairAttempts > 10 {
		return fmt.Errorf("ideation history must not exceed 4000 tokens per retrieval or 12 records; novelty repairs must not exceed 10")
	}
	perRetrieval, attempts := s.HistoryTokenBudget, s.NoveltyRepairAttempts
	if perRetrieval == 0 {
		perRetrieval = 4000
	}
	if attempts == 0 {
		attempts = 3
	}
	if s.HistoryRunTokenBudget > perRetrieval*attempts*2 {
		return fmt.Errorf("ideation total history budget cannot exceed %d tokens for %d generation and novelty retrieval pairs", perRetrieval*attempts*2, attempts)
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

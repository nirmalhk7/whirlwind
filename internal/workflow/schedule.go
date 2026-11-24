package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

type IdeationSchedule struct {
	MaxAttemptsPerDirection int `json:"max_attempts_per_direction"`
	MaxCandidates           int `json:"max_candidates,omitempty"`
	HistoryTokenBudget      int `json:"history_token_budget,omitempty"`
	HistoryRunTokenBudget   int `json:"history_run_token_budget,omitempty"`
	HistoryRecordLimit      int `json:"history_record_limit,omitempty"`
	// NoveltyRepairAttempts preserves the persisted settings key; it bounds
	// content-validation repair attempts and does not run uniqueness checks.
	NoveltyRepairAttempts int    `json:"novelty_repair_attempts,omitempty"`
	Cron                  string `json:"cron"`
}

func DefaultSettings() Settings {
	return Settings{GlobalSlots: 4, ProjectSlots: 2, ReconciliationSeconds: 300, ProviderStatusCheckSeconds: 30, Ideation: IdeationSchedule{MaxAttemptsPerDirection: 5, Cron: "CRON_TZ=America/Denver 0 10 * * *", HistoryTokenBudget: 4000, HistoryRunTokenBudget: 12000, HistoryRecordLimit: 12, NoveltyRepairAttempts: 3}}
}

// UnmarshalJSON accepts older persisted daily settings and writes them back as cron.
func (s *IdeationSchedule) UnmarshalJSON(data []byte) error {
	type scheduleFields IdeationSchedule
	decoded := scheduleFields(*s)
	input := struct {
		*scheduleFields
		Cron    *string `json:"cron"`
		Enabled *bool   `json:"enabled"`
		Time    *string `json:"time"`
		Zone    *string `json:"timezone"`
	}{scheduleFields: &decoded}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	switch {
	case input.Cron != nil:
		if input.Enabled != nil || input.Time != nil || input.Zone != nil {
			return fmt.Errorf("ideation cron cannot be combined with legacy schedule fields")
		}
		decoded.Cron = *input.Cron
	case input.Enabled != nil || input.Time != nil || input.Zone != nil:
		expression, err := legacyIdeationCron(input.Enabled, input.Time, input.Zone)
		if err != nil {
			return err
		}
		decoded.Cron = expression
	}
	*s = IdeationSchedule(decoded)
	return nil
}

func legacyIdeationCron(enabled *bool, clockValue, timezone *string) (string, error) {
	clock, zone := "10:00", "America/Denver"
	if clockValue != nil {
		clock = *clockValue
	}
	if timezone != nil {
		zone = *timezone
	}
	at, err := time.Parse("15:04", clock)
	if err != nil {
		return "", fmt.Errorf("legacy ideation time must be HH:MM")
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" {
		return "", fmt.Errorf("invalid legacy ideation timezone %q", zone)
	}
	if enabled != nil && !*enabled {
		return "", nil
	}
	return fmt.Sprintf("CRON_TZ=%s %d %d * * *", zone, at.Minute(), at.Hour()), nil
}

func (s IdeationSchedule) Validate() error {
	if s.MaxAttemptsPerDirection < 0 || s.MaxAttemptsPerDirection > 5 {
		return fmt.Errorf("max_attempts_per_direction must be between 1 and 5 when configured")
	}
	if s.MaxCandidates < 0 || s.MaxCandidates > 12 {
		return fmt.Errorf("max_candidates must be between 1 and 12 when configured")
	}
	if s.HistoryTokenBudget < 0 || s.HistoryTokenBudget > 4000 || s.HistoryRunTokenBudget < 0 || s.HistoryRecordLimit < 0 || s.HistoryRecordLimit > 12 || s.NoveltyRepairAttempts < 0 || s.NoveltyRepairAttempts > 10 {
		return fmt.Errorf("ideation history must not exceed 4000 tokens per retrieval or 12 records; repair attempts must not exceed 10")
	}
	perRetrieval, attempts := s.HistoryTokenBudget, s.NoveltyRepairAttempts
	if perRetrieval == 0 {
		perRetrieval = 4000
	}
	if attempts == 0 {
		attempts = 3
	}
	if s.HistoryRunTokenBudget > perRetrieval*attempts {
		return fmt.Errorf("ideation total history budget cannot exceed %d tokens for %d bounded generation attempts", perRetrieval*attempts, attempts)
	}
	_, err := s.cronSpec()
	return err
}

var ideationCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (s IdeationSchedule) cronSpec() (*cron.SpecSchedule, error) {
	fields := strings.Fields(s.Cron)
	if len(fields) == 0 {
		return nil, nil
	}
	scheduleFields := fields
	if zone, ok := strings.CutPrefix(fields[0], "CRON_TZ="); ok {
		if zone == "" || zone == "Local" {
			return nil, fmt.Errorf("ideation cron requires an IANA time zone after CRON_TZ=")
		}
		scheduleFields = fields[1:]
	}
	if len(scheduleFields) != 5 || strings.Contains(s.Cron, "?") {
		return nil, fmt.Errorf("ideation cron requires five crontab fields")
	}
	schedule, err := ideationCronParser.Parse(strings.Join(fields, " "))
	if err != nil {
		return nil, fmt.Errorf("invalid ideation cron: %w", err)
	}
	return schedule.(*cron.SpecSchedule), nil
}

// latestOccurrence catches up only the most recent matching minute.
// The parser provides a forward search, so bracket and bisect the last match.
func (s IdeationSchedule) latestOccurrence(now time.Time) (time.Time, error) {
	spec, err := s.cronSpec()
	if err != nil || spec == nil {
		return time.Time{}, err
	}
	upper := now.In(spec.Location).Truncate(time.Minute)
	for years := 1; years <= 8; years++ {
		lower := upper.AddDate(-years, 0, 0).Add(-time.Minute)
		first := spec.Next(lower)
		if first.IsZero() || first.After(now) {
			continue
		}
		offset := sort.Search(int(upper.Sub(lower)/time.Minute)+1, func(i int) bool {
			next := spec.Next(lower.Add(time.Duration(i) * time.Minute))
			return next.IsZero() || next.After(now)
		})
		return spec.Next(lower.Add(time.Duration(offset-1) * time.Minute)), nil
	}
	return time.Time{}, nil
}

// DueDay supplies the display date for manually queued ideation.
func (s IdeationSchedule) DueDay(now time.Time) (string, error) {
	slot, err := s.latestOccurrence(now)
	if err != nil {
		return "", err
	}
	if slot.IsZero() {
		slot = now.In(time.Local)
	}
	return slot.Format("2006-01-02"), nil
}

func (e *Engine) IdeationDay(ctx context.Context, now time.Time) (string, error) {
	return e.settings(ctx).Ideation.DueDay(now)
}

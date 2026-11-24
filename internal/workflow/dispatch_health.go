package workflow

import (
	"context"
	"strings"
	"unicode"

	"whirlwind/internal/model"
	"whirlwind/internal/redaction"
)

func (e *Engine) DispatchHealth() model.DispatchHealth {
	if state := e.dispatchHealth.Load(); state != nil {
		return *state
	}
	return model.DispatchHealth{}
}

func (e *Engine) checkDispatchRecovery(ctx context.Context) error {
	err := e.reconcileStandaloneRuntimes(ctx)
	if ctx.Err() == nil {
		e.dispatchHealth.Store(&model.DispatchHealth{Checked: true, BlockReason: dispatchRecoveryReason(err)})
	}
	return err
}

func dispatchRecoveryReason(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, redaction.RedactText(err.Error()))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "Sandbox recovery could not be verified"
	}
	const maxRunes = 800
	if clean := []rune(text); len(clean) > maxRunes {
		return string(clean[:maxRunes]) + "…"
	}
	return text
}

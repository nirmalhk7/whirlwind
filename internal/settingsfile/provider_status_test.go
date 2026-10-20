package settingsfile

import (
	"strings"
	"testing"
)

func TestProviderStatusIntervalDefaultsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		want        int
		invalid     bool
	}{
		{"default", "", 30, false},
		{"configured", "  provider_status_check_seconds: 60\n", 60, false},
		{"zero", "  provider_status_check_seconds: 0\n", 0, true},
		{"negative", "  provider_status_check_seconds: -1\n", 0, true},
		{"too long", "  provider_status_check_seconds: 86401\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := decode([]byte(strings.Replace(editorYAML, "  global_slots: 4\n", "  global_slots: 4\n"+tc.field, 1)))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid interval accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if doc.Workers.ProviderStatusCheckSeconds != tc.want {
				t.Fatalf("interval=%d want %d", doc.Workers.ProviderStatusCheckSeconds, tc.want)
			}
		})
	}
}

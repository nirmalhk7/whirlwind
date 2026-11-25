package settingsfile

import (
	"strings"
	"testing"
)

func TestSettingsAcceptAndPersistCrontabIdeation(t *testing.T) {
	for _, tt := range []struct{ yaml, want string }{
		{`{cron: "CRON_TZ=America/Denver 0 9,10 * * MON-FRI"}`, "CRON_TZ=America/Denver 0 9,10 * * MON-FRI"},
		{`{cron: ""}`, `cron: ""`},
		{`{enabled: true, time: "09:30", timezone: America/Denver}`, "CRON_TZ=America/Denver 30 9 * * *"},
		{`{enabled: false, time: "09:30", timezone: America/Denver}`, `cron: ""`},
	} {
		input := strings.Replace(editorYAML, "  global_slots: 4", "  global_slots: 4\n  ideation: "+tt.yaml, 1)
		doc, err := decode([]byte(input))
		if err != nil {
			t.Fatalf("%s: %v", tt.yaml, err)
		}
		output, err := encode(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, legacy := range []string{"enabled:", "time:", "timezone:"} {
			if strings.Contains(string(output), legacy) {
				t.Fatalf("settings retained legacy schedule field %s", legacy)
			}
		}
		if !strings.Contains(string(output), tt.want) {
			t.Fatalf("%s: settings lost %q", tt.yaml, tt.want)
		}
	}
}

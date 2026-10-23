package settingsfile

import (
	"testing"
	"whirlwind/internal/model"
)

func TestIntegrationRoutingValidation(t *testing.T) {
	providers := map[string]Provider{"codex": {ID: "codex", Kind: "codex"}}
	valid := model.IntegrationSettings{Discovery: "connected", Sources: []model.IntegrationSource{{Provider: "codex", Ref: "app://ci", Scopes: []string{"repo"}}}, Publication: map[string]model.IntegrationTarget{"https://git.example/team/repo": {Provider: "codex", Ref: "app://git"}}}
	if err := validateIntegrations(valid, providers); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []model.IntegrationSettings{
		{Sources: []model.IntegrationSource{{Provider: "missing", Ref: "app://ci"}}},
		{Sources: []model.IntegrationSource{{Provider: "codex"}}},
		{Sources: []model.IntegrationSource{{Provider: "codex", Ref: "app://ci"}, {Provider: "codex", Ref: "app://ci"}}},
		{Publication: map[string]model.IntegrationTarget{"git@git.example:team/repo.git": {Provider: "codex", Ref: "app://git"}}},
		{Publication: map[string]model.IntegrationTarget{"https://git.example/team/repo": {Provider: "codex"}}},
	} {
		if err := validateIntegrations(bad, providers); err == nil {
			t.Fatalf("accepted invalid routing: %+v", bad)
		}
	}
}

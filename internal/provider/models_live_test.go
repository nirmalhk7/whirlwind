package provider

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in metadata discovery against installed, authenticated ACP adapters; no prompt.
func TestACPModelCatalogLive(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_ACP_MODELS_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_ACP_MODELS_LIVE=1 to verify ACP model discovery")
	}
	for _, index := range []int{0, 2} {
		p := Defaults("")[index]
		t.Run(p.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			catalog, err := Models(ctx, p, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.Models) == 0 {
				t.Fatal("ACP returned no models")
			}
			for _, model := range catalog.Models {
				if model.ID == "" {
					t.Fatal("ACP returned empty model ID")
				}
			}
			t.Logf("ACP advertised %d models; current=%s", len(catalog.Models), catalog.Current)
		})
	}
}

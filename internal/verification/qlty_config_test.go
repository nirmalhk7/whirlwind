package verification

import "testing"

func TestQltyConfigurationChangeDetection(t *testing.T) {
	if !ChangesQltyConfiguration([]string{".qlty/qlty.toml"}) {
		t.Fatal("Qlty rule configuration edit was not detected")
	}
	if !ChangesQltyConfiguration([]string{".qltyignore"}) {
		t.Fatal("Qlty ignore edit was not detected")
	}
	if ChangesQltyConfiguration([]string{"internal/workflow/verify.go", "README.md"}) {
		t.Fatal("ordinary source change was treated as Qlty configuration")
	}
}

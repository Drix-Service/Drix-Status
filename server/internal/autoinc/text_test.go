package autoinc

import (
	"strings"
	"testing"
)

// Every status page language has all four texts, with the names where they go.
func TestTexts(t *testing.T) {
	langs := []string{"en", "es", "fr", "de", "pt", "it", "nl", "pl", "da", "sv", "nb", "fi", "ja", "zh", "ko"}
	if len(byLang) != len(langs) {
		t.Fatalf("%d languages, want %d", len(byLang), len(langs))
	}
	for _, l := range langs {
		tx, ok := byLang[l]
		if !ok {
			t.Fatalf("%s missing", l)
		}
		for name, s := range map[string]string{"title": tx.Title, "investigating": tx.Investigating, "also": tx.AlsoAffected} {
			if !strings.Contains(s, "{names}") {
				t.Errorf("%s %s has no {names}: %q", l, name, s)
			}
		}
		if tx.Resolved == "" || strings.Contains(tx.Resolved, "{") {
			t.Errorf("%s resolved text: %q", l, tx.Resolved)
		}
	}
	if got := fill(textsFor("xx").Title, []string{"API", "Website"}); got != "Problems with API, Website" {
		t.Errorf("unknown language falls back to English: %q", got)
	}
	if got := fill(textsFor("de").Investigating, []string{"API"}); got != "Wir untersuchen Probleme mit API." {
		t.Errorf("german: %q", got)
	}
}

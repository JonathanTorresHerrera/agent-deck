package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeatDisplayLabel(t *testing.T) {
	d := t.TempDir()
	h := filepath.Join(d, "h")
	os.MkdirAll(h, 0o755)
	os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"x@y.com","accessToken":"no"}}`), 0o600)
	os.WriteFile(filepath.Join(d, "accounts.json"), []byte(`{"slots":[{"id":"a","provider":"claude","deckAccount":"claude-2"},{"id":"b","provider":"codex","deckAccount":""},{"id":"c","provider":"claude","deckAccount":"claude-70227a42","home":"`+h+`"}]}`), 0o600)
	t.Setenv("AGENT_DECK_JEV_ACCOUNTS_FILE", filepath.Join(d, "accounts.json"))
	for in, want := range map[string]string{"claude-70227a42": "claude-2 (x@y.com)", "claude-2": "claude-1", "zzz": "zzz"} {
		if g := seatDisplayLabel(in); g != want {
			t.Fatalf("%s: %q", in, g)
		}
	}
	if p := newAccountPresentation("claude-70227a42", true); p.compact != " @claude-2 (x@y.com)" {
		t.Fatal(p.compact)
	}
}

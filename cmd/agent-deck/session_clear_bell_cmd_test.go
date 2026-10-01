package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// Patch 29: `session clear-bell` end to end through the real binary.
func TestPatch29_SessionClearBellCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess CLI test skipped in short mode")
	}
	home := t.TempDir()
	projectDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const profile = "p27"
	stdout, stderr, code := runAgentDeck(t, home, "-p", profile, "add", "-t", "bell-test", "-c", "claude", "--no-parent", "--json", projectDir)
	if code != 0 {
		t.Fatalf("add failed (%d): %s %s", code, stdout, stderr)
	}
	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &added); err != nil {
		t.Fatal(err)
	}
	clear := func(args ...string) (map[string]any, int) {
		t.Helper()
		out, errOut, code := runAgentDeck(t, home, append([]string{"-p", profile, "session", "clear-bell"}, append(args, "--json")...)...)
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("clear-bell output not JSON (%d): %s %s", code, out, errOut)
		}
		return m, code
	}

	// No ask yet.
	if m, code := clear(added.ID); code != 0 || m["result"] != "no_bell" {
		t.Fatalf("no_bell: code=%d %v", code, m)
	}

	// Seed a waiting ask straight into the profile's DB.
	db, err := statedb.Open(filepath.Join(home, ".local", "share", "agent-deck", "profiles", profile, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WriteLastAidaAsk(added.ID, []byte(`{"at":1790000000,"status":"accepted","ref":"doorbell-7","channel":"devy/#aida-ops","fallback":false}`)); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// show exposes the waiting-state object (the 48 h window has passed for this fixture).
	out, _, _ := runAgentDeck(t, home, "-p", profile, "session", "show", added.ID, "--json")
	var shown struct {
		AidaAsk map[string]any `json:"aida_ask"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.AidaAsk["ref"] != "doorbell-7" || shown.AidaAsk["answered_at"] != nil {
		t.Fatalf("show aida_ask = %v (%v)", shown.AidaAsk, err)
	}

	// Bad input is refused before anything is written.
	if m, code := clear(added.ID, "--by", "-x"); code == 0 || m["success"] != false {
		t.Errorf("bad --by accepted: %v", m)
	}
	if m, code := clear(added.ID, "--note", "a\nb"); code == 0 || m["success"] != false {
		t.Errorf("newline note accepted: %v", m)
	}
	if m, code := clear(added.ID, "--reason", "other"); code == 0 || m["success"] != false {
		t.Errorf("bad reason accepted: %v", m)
	}

	m, code := clear(added.ID, "--by", "aida", "--reason", "message", "--note", "replied on the bus")
	if code != 0 || m["result"] != "cleared" || m["cleared_by"] != "aida" || m["clear_reason"] != "message" || m["ref"] != "doorbell-7" || m["note"] != "replied on the bus" {
		t.Fatalf("clear: code=%d %v", code, m)
	}
	if m, code := clear("bell-test"); code != 0 || m["result"] != "already_answered" || m["cleared_by"] != "aida" {
		t.Fatalf("second clear by title: code=%d %v", code, m)
	}
	out, _, _ = runAgentDeck(t, home, "-p", profile, "session", "show", added.ID, "--json")
	_ = json.Unmarshal([]byte(out), &shown)
	if shown.AidaAsk["cleared_by"] != "aida" || shown.AidaAsk["waiting"] != false || shown.AidaAsk["answered_at"] == nil {
		t.Errorf("show after clear = %v", shown.AidaAsk)
	}

	if _, _, code := runAgentDeck(t, home, "-p", profile, "session", "clear-bell", "no-such-session", "--json"); code != 2 {
		t.Errorf("unknown session exit = %d, want 2", code)
	}
	help, _, _ := runAgentDeck(t, home, "session", "help")
	if !strings.Contains(help, "clear-bell") {
		t.Error("session help does not list clear-bell")
	}
}

// Patch 20: the Deck's own instance ID is visible and copyable.
//
// The preview showed only the tool's session ID (Claude/Codex/...), never the
// Deck's own ID, and the Shift+C block carried the tool ID and not the Deck
// ID. The Deck ID is the exact key Aida looks sessions up by
// (agent-deck-mcp id_or_title; exact IDs take precedence), so a paste to her
// should carry it. Requested 2026-09-26.
package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestBuildSessionInfoForCopy_CarriesDeckIDAfterName(t *testing.T) {
	inst := &session.Instance{
		ID:              "9ea27510-1790439281",
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}
	lines := strings.Split(buildSessionInfoForCopy(inst), "\n")
	if len(lines) < 2 || lines[0] != "Name: Agent-Deck" || lines[1] != "Deck ID: 9ea27510-1790439281" {
		t.Fatalf("want Name then Deck ID as the first two lines, got:\n%s", strings.Join(lines, "\n"))
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"Path: /home/jtorres", "Session: 7e60e581-5c76-4523-ae62-5de8741cd18c"} {
		if !strings.Contains(got, want) {
			t.Errorf("adding the Deck ID dropped %q, got:\n%s", want, got)
		}
	}
}

func TestBuildSessionInfoForCopy_NoDeckIDLabelWhenBlank(t *testing.T) {
	inst := &session.Instance{Title: "x", ProjectPath: "/tmp/scratch"}
	if got := buildSessionInfoForCopy(inst); strings.Contains(got, "Deck ID:") {
		t.Errorf("blank ID produced a Deck ID line, got:\n%s", got)
	}
}

func TestDeckIDPreviewLine(t *testing.T) {
	if got := deckIDPreviewLine(&session.Instance{ID: "9ea27510-1790439281"}); got != "🆔 Deck ID: 9ea27510-1790439281" {
		t.Errorf("preview line = %q", got)
	}
	if got := deckIDPreviewLine(&session.Instance{}); got != "" {
		t.Errorf("blank ID should render nothing, got %q", got)
	}
}

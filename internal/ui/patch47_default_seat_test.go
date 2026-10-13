package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch47_DefaultSeatLabel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for tool, want := range map[string]string{
		"claude": "claude",
		"codex":  "codex",
		"grok":   "grok",
		"shell":  "",
		"":       "",
	} {
		if got := defaultSeatLabel(tool, "Aida"); got != want {
			t.Fatalf("defaultSeatLabel(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestPatch47_UnsetSeatRowShowsDefaultSeat(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140, showInheritedAccount: true}
	inst := &session.Instance{ID: "x", Title: "WO-OPS-CI-SEARCH-01", Tool: "claude"}
	state := sessionRenderState{
		status: session.StatusIdle, tool: "claude", title: inst.Title,
		defaultSeat: "claude", accountDisplay: seatPresentation("", "claude", true),
	}
	row := ansi.Strip(p31RenderRow(h, inst, state, 100))
	if !strings.Contains(row, " @claude") || strings.Contains(row, "inherited") {
		t.Fatalf("unset seat should read @claude: %q", row)
	}
	// The setting still hides it.
	h.showInheritedAccount = false
	if row := ansi.Strip(p31RenderRow(h, inst, state, 100)); strings.Contains(row, "@") {
		t.Fatalf("show_inherited_account=false must hide the default seat: %q", row)
	}
}

func TestPatch47_StoredSeatWins(t *testing.T) {
	if got := seatPresentation("claude-3", "claude", true).compact; got != " @claude-3" {
		t.Fatalf("stored seat = %q", got)
	}
	if got := seatPresentation("", "", true).compact; got != " @inherited" {
		t.Fatalf("no default seat keeps the old inherited form: %q", got)
	}
}

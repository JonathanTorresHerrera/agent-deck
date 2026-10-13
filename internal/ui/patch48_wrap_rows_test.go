package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func p48State(title, account string) sessionRenderState {
	return sessionRenderState{
		status: session.StatusIdle, tool: "claude", title: title,
		account: account, accountDisplay: newAccountPresentation(account, true),
	}
}

func TestPatch48_WrappedRowPutsTitleAloneOnLineOne(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140, wrapRows: true, showSessionTimestamps: true}
	title := "WO-OPS-INSPECT-EXPECT-01 meeting commitments to boards"
	inst := &session.Instance{ID: "p48", Title: title, Account: "claude-3"}
	out := ansi.Strip(p31RenderRow(h, inst, p48State(title, "claude-3"), 80))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], title) || strings.Contains(lines[0], "@") {
		t.Fatalf("line 1 must be the whole title and nothing else: %q", lines[0])
	}
	if !strings.Contains(lines[1], "@claude-3") {
		t.Fatalf("line 2 must carry the seat: %q", lines[1])
	}
	// The details start in the title's column.
	titleCol := strings.Index(lines[0], "WO-OPS")
	seatCol := strings.Index(lines[1], "@claude-3")
	if ansi.StringWidth(lines[0][:titleCol]) != ansi.StringWidth(lines[1][:seatCol]) {
		t.Fatalf("seat not aligned under title:\n%q\n%q", lines[0], lines[1])
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > 80 {
			t.Fatalf("line %d is %d cells, list is 80: %q", i+1, ansi.StringWidth(l), l)
		}
	}
}

func TestPatch48_OffKeepsOneLine(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140}
	inst := &session.Instance{ID: "p48", Title: "WO-X", Account: "claude-3"}
	out := p31RenderRow(h, inst, p48State("WO-X", "claude-3"), 80)
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("wrap off must stay one line: %q", out)
	}
}

func TestPatch48_HeightsMatchRenderer(t *testing.T) {
	inst := &session.Instance{ID: "s", Title: "t"}
	items := []session.Item{
		{Type: session.ItemTypeGroup, Path: "g"},
		{Type: session.ItemTypeSession, Session: inst, Level: 1},
		{Type: session.ItemTypeSession, CreatingID: "c", Level: 1},
		{Type: session.ItemTypeSession, Session: inst, Level: 1, IsLastInGroup: true},
	}
	h := &Home{wrapRows: true}
	if got := h.sidebarItemsHeightWithLines(items, 0, len(items), 80, 1); got != 1+2+1+2 {
		t.Fatalf("wrapped list height = %d, want 6", got)
	}
	h.wrapRows = false
	if got := h.sidebarItemsHeightWithLines(items, 0, len(items), 80, 1); got != 4 {
		t.Fatalf("unwrapped list height = %d, want 4", got)
	}
}

func TestPatch48_LastRowHasNoTreeContinuation(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140, wrapRows: true}
	inst := &session.Instance{ID: "p48", Title: "WO-LAST"}
	// p31RenderRow marks the row last in its group: no │ under └─.
	out := ansi.Strip(p31RenderRow(h, inst, p48State("WO-LAST", ""), 80))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if strings.Contains(lines[1], "│") {
		t.Fatalf("last row's detail line must not continue the tree: %q", lines[1])
	}
	item := session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1}
	var b strings.Builder
	h.renderSessionItem(&b, item, false, map[string]sessionRenderState{inst.ID: p48State("WO-LAST", "")}, 80)
	lines = strings.Split(strings.TrimSuffix(ansi.Strip(b.String()), "\n"), "\n")
	if !strings.Contains(lines[1], "│") {
		t.Fatalf("a middle row's detail line continues the tree: %q", lines[1])
	}
}

func TestPatch48_SettingToggles(t *testing.T) {
	s := &SettingsPanel{cursor: int(SettingWrapRows)}
	if !s.toggleValue() || !s.wrapRows {
		t.Fatalf("toggling Wrap rows should turn it on")
	}
}

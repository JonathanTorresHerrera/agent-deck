// #1987 follow-up: the fix landed on the sidebar header only.
//
// buildGroupRenderStats was taught to count just the partition being rendered
// (see issue1987_group_header_counts_test.go). renderGroupPreview was not — it
// still reads the raw group.Sessions slice, which holds both archive
// partitions. So the two surfaces describing the SAME group disagree.
//
// Reported on the live deck 2026-09-17: sidebar `Vita-EHR (14)` beside a
// preview reading `37 sessions`. Measured from the deck: the group holds 37
// rows, 14 not archived (12 waiting + 2 running) and 23 archived. 14 + 23 = 37.
// Neither number was wrong; they counted different populations, and only one
// of them matched the rows on screen.
//
// The preview's status breakdown is wrong for the second reason the #1987
// comment documents: archiving does not reset Status and the status updater
// skips archived sessions, so an archived row contributes whatever it was
// doing when it was archived, forever. That is where the reporter's "7 error"
// came from with no error rows visible anywhere.
package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// previewFixture mirrors the shape of the reported group: a handful of active
// rows beside a larger pile of archived ones, with the archived rows still
// carrying live-looking statuses.
func previewFixture() []*session.Instance {
	return []*session.Instance{
		{ID: "a1", Title: "ACTIVE-ONE", GroupPath: "demo", Tool: "claude", Status: session.StatusWaiting},
		{ID: "a2", Title: "ACTIVE-TWO", GroupPath: "demo", Tool: "claude", Status: session.StatusRunning},
		{ID: "z1", Title: "ARCHIVED-ONE", GroupPath: "demo", Tool: "claude", Status: session.StatusStopped, ArchivedAt: archivedAt()},
		{ID: "z2", Title: "ARCHIVED-TWO", GroupPath: "demo", Tool: "claude", Status: session.StatusError, ArchivedAt: archivedAt()},
		{ID: "z3", Title: "ARCHIVED-THREE", GroupPath: "demo", Tool: "claude", Status: session.StatusStopped, ArchivedAt: archivedAt()},
	}
}

func renderDemoPreview(t *testing.T, h *Home) string {
	t.Helper()
	g := h.groupTree.Groups["demo"]
	if g == nil {
		t.Fatal("fixture group 'demo' missing from the tree")
	}
	return tmux.StripANSI(h.renderGroupPreview(g, 80, 40))
}

// The headline count must describe the rows the deck is actually showing —
// the same population the sidebar header counts.
func TestRenderGroupPreview_CountsOnlyTheRenderedPartition(t *testing.T) {
	h := &Home{groupTree: session.NewGroupTree(previewFixture())}

	out := renderDemoPreview(t, h)
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("active view: preview should read '2 sessions' (the 2 unarchived rows), got:\n%s", out)
	}
	if strings.Contains(out, "5 sessions") {
		t.Errorf("active view: preview counted BOTH partitions ('5 sessions') — this is the sidebar/preview disagreement (#1987 follow-up):\n%s", out)
	}

	// In the archived view (^) the archived rows ARE the rows on screen, so the
	// preview must follow the partition rather than excluding archived blindly.
	h.statusFilter = FilterModeArchived
	out = renderDemoPreview(t, h)
	if !strings.Contains(out, "3 sessions") {
		t.Errorf("archived view: preview should read '3 sessions', got:\n%s", out)
	}
}

// The listing under "Sessions" must not show rows that are not reachable in
// the current view — that is what made the reporter suspect phantom sessions.
func TestRenderGroupPreview_ListsOnlyTheRenderedPartition(t *testing.T) {
	h := &Home{groupTree: session.NewGroupTree(previewFixture())}

	out := renderDemoPreview(t, h)
	for _, want := range []string{"ACTIVE-ONE", "ACTIVE-TWO"} {
		if !strings.Contains(out, want) {
			t.Errorf("active view: preview is missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"ARCHIVED-ONE", "ARCHIVED-TWO", "ARCHIVED-THREE"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("active view: preview lists archived row %q, which has no row in the sidebar", unwanted)
		}
	}

	h.statusFilter = FilterModeArchived
	out = renderDemoPreview(t, h)
	if !strings.Contains(out, "ARCHIVED-ONE") {
		t.Errorf("archived view: preview should list the archived rows:\n%s", out)
	}
	if strings.Contains(out, "ACTIVE-ONE") {
		t.Errorf("archived view: preview lists an unarchived row, which has no row in this view")
	}
}

// The status breakdown is the line that produced "✕ 7 error" against a group
// with no reachable error rows: archived sessions keep a frozen Status.
func TestRenderGroupPreview_StatusBreakdownIgnoresTheOtherPartition(t *testing.T) {
	h := &Home{groupTree: session.NewGroupTree(previewFixture())}

	out := renderDemoPreview(t, h)
	if strings.Contains(out, "error") {
		t.Errorf("active view: breakdown reports an error status from an ARCHIVED row that has no process and no row:\n%s", out)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("active view: breakdown reports stopped rows that are all archived:\n%s", out)
	}
	if !strings.Contains(out, "1 running") || !strings.Contains(out, "1 waiting") {
		t.Errorf("active view: breakdown should read 1 running / 1 waiting, got:\n%s", out)
	}
}

// An empty partition must read as empty, not borrow the other partition's rows.
func TestRenderGroupPreview_EmptyPartitionReadsEmpty(t *testing.T) {
	onlyArchived := []*session.Instance{
		{ID: "z1", Title: "ARCHIVED-ONE", GroupPath: "demo", Tool: "claude", Status: session.StatusStopped, ArchivedAt: archivedAt()},
	}
	h := &Home{groupTree: session.NewGroupTree(onlyArchived)}

	out := renderDemoPreview(t, h)
	if !strings.Contains(out, "No sessions in this group") {
		t.Errorf("a group whose only rows are archived must read empty in the active view, got:\n%s", out)
	}
	if !strings.Contains(out, "0 sessions") {
		t.Errorf("active view: expected '0 sessions', got:\n%s", out)
	}
}

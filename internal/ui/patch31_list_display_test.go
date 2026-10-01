package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 31 (settings density): behaviour of the left-list presentation
// settings — tool label, inherited-account tag, empty groups, density.

func p31RenderRow(h *Home, inst *session.Instance, state sessionRenderState, listWidth int) string {
	item := session.Item{
		Type:          session.ItemTypeSession,
		Session:       inst,
		Level:         1,
		Path:          "test",
		IsLastInGroup: true,
	}
	var b strings.Builder
	h.renderSessionItem(&b, item, false, map[string]sessionRenderState{inst.ID: state}, listWidth)
	return b.String()
}

func TestPatch31_ToolLabelHiddenByDefaultShownWhenEnabled(t *testing.T) {
	forceTrueColorProfile()
	inst := &session.Instance{ID: "p31-tool", Title: "my-session"}
	state := sessionRenderState{status: session.StatusIdle, tool: "claude"}

	h := &Home{width: 140}
	if row := p31RenderRow(h, inst, state, h.width); strings.Contains(row, "claude") {
		t.Fatalf("tool label must be hidden by default. Got: %q", row)
	}

	h.showToolLabel = true
	if row := p31RenderRow(h, inst, state, h.width); !strings.Contains(row, "claude") {
		t.Fatalf("show_tool_label=true must render the tool name. Got: %q", row)
	}
}

func TestPatch31_HiddenToolLabelReclaimsWidthForTitle(t *testing.T) {
	forceTrueColorProfile()
	inst := &session.Instance{ID: "p31-width", Title: strings.Repeat("x", 80)}
	state := sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}

	hidden := &Home{width: 40}
	shown := &Home{width: 40, showToolLabel: true}
	hiddenRow := p31RenderRow(hidden, inst, state, 40)
	shownRow := p31RenderRow(shown, inst, state, 40)

	if got := strings.Count(hiddenRow, "x"); got <= strings.Count(shownRow, "x") {
		t.Fatalf("hiding the tool label must give the title more cells: hidden=%d shown=%d x's",
			got, strings.Count(shownRow, "x"))
	}
	if cellWidth(hiddenRow) > 40 || cellWidth(shownRow) > 40 {
		t.Fatalf("rows must still fit listWidth 40: hidden=%d shown=%d", cellWidth(hiddenRow), cellWidth(shownRow))
	}
}

func TestPatch31_InheritedAccountHiddenNamedAlwaysShown(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140}

	inherited := &session.Instance{ID: "p31-inh", Title: "inherits"}
	inhState := sessionRenderState{
		status: session.StatusIdle, tool: "claude",
		account: "", accountDisplay: newAccountPresentation("", true),
	}
	if row := p31RenderRow(h, inherited, inhState, h.width); strings.Contains(row, "[account:") {
		t.Fatalf("inherited tag must be hidden by default. Got: %q", row)
	}

	named := &session.Instance{ID: "p31-named", Title: "named", Account: "claude-2"}
	namedState := sessionRenderState{
		status: session.StatusIdle, tool: "claude",
		account: "claude-2", accountDisplay: newAccountPresentation("claude-2", true),
	}
	if row := p31RenderRow(h, named, namedState, h.width); !strings.Contains(row, `[account:"claude-2"]`) {
		t.Fatalf("a named account badge must always show. Got: %q", row)
	}

	h.showInheritedAccount = true
	if row := p31RenderRow(h, inherited, inhState, h.width); !strings.Contains(row, "[account:inherited]") {
		t.Fatalf("show_inherited_account=true must render the inherited tag. Got: %q", row)
	}
}

func TestPatch31_CompactAgeIsLargestUnitNoAgo(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{45 * time.Minute, "45m"},
		{2*time.Hour + 6*time.Minute, "2h"},
		{3*24*time.Hour + 5*time.Hour, "3d"},
		{15 * 24 * time.Hour, "2w"},
		{65 * 24 * time.Hour, "2mo"},
		{800 * 24 * time.Hour, "2y"},
		{-time.Minute, "now"},
	}
	for _, tc := range cases {
		if got := humanizeSinceCompact(tc.d); got != tc.want {
			t.Errorf("humanizeSinceCompact(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestPatch31_CompactDensityRowUsesShortAge(t *testing.T) {
	forceTrueColorProfile()
	created := time.Now().Add(-(2*time.Hour + 6*time.Minute))
	inst := &session.Instance{ID: "p31-age", Title: "aged", CreatedAt: created}
	state := sessionRenderState{status: session.StatusIdle, tool: "claude"}

	comfortable := &Home{width: 140, showSessionTimestamps: true}
	if row := p31RenderRow(comfortable, inst, state, comfortable.width); !strings.Contains(row, "2h 6m ago") {
		t.Fatalf("comfortable density must keep the two-component age. Got: %q", row)
	}

	compact := &Home{width: 140, showSessionTimestamps: true, listDensity: session.DensityCompact}
	row := p31RenderRow(compact, inst, state, compact.width)
	if strings.Contains(row, "ago") || strings.Contains(row, "6m") {
		t.Fatalf("compact density must drop 'ago' and the second unit. Got: %q", row)
	}
	if !strings.Contains(row, " 2h") {
		t.Fatalf("compact density must render the largest unit ('2h'). Got: %q", row)
	}
}

// p31Home builds a Home over two populated groups (alpha: a1,a2; beta: b1)
// plus one empty top-level group ("zeta") under an isolated HOME.
func p31Home(t *testing.T) *Home {
	t.Helper()
	isolatePatch31Config(t)
	home := NewHome()
	home.width = 120
	home.height = 40
	home.initialLoading = false

	a1 := session.NewInstanceWithTool("a1", "/tmp/a1", "claude")
	a2 := session.NewInstanceWithTool("a2", "/tmp/a2", "claude")
	b1 := session.NewInstanceWithTool("b1", "/tmp/b1", "claude")
	a1.GroupPath, a2.GroupPath, b1.GroupPath = "alpha", "alpha", "beta"
	instances := []*session.Instance{a1, a2, b1}

	home.instancesMu.Lock()
	home.instances = instances
	home.instancesMu.Unlock()
	home.groupTree = session.NewGroupTree(instances)
	home.groupTree.CreateGroup("zeta")
	home.rebuildFlatItems()
	return home
}

func p31GroupIndex(h *Home, name string) int {
	for i, it := range h.flatItems {
		if it.Type == session.ItemTypeGroup && it.Group != nil && it.Group.Name == name {
			return i
		}
	}
	return -1
}

func TestPatch31_EmptyGroupsHiddenByDefaultSiblingKept(t *testing.T) {
	home := p31Home(t)

	if p31GroupIndex(home, "zeta") >= 0 {
		t.Fatal("empty group must be hidden from the list by default")
	}
	if p31GroupIndex(home, "alpha") < 0 || p31GroupIndex(home, "beta") < 0 {
		t.Fatal("non-empty groups must stay visible")
	}
	// The tree still holds the group, so n / N / g targeting by path is unaffected.
	if home.groupTree.Groups["zeta"] == nil {
		t.Fatal("hiding must not remove the group from groupTree")
	}

	home.showEmptyGroups = true
	home.rebuildFlatItems()
	if p31GroupIndex(home, "zeta") < 0 {
		t.Fatal("show_empty_groups=true must keep the empty group in the list")
	}
}

// A group created in this run (via the `g` dialog) stays visible while empty.
func TestPatch31_GroupCreatedThisRunStaysVisibleWhileEmpty(t *testing.T) {
	home := p31Home(t)
	if p31GroupIndex(home, "zeta") >= 0 {
		t.Fatal("fixture: empty group must start hidden")
	}
	home.keepEmptyGroup("zeta")
	home.rebuildFlatItems()
	if p31GroupIndex(home, "zeta") < 0 {
		t.Fatal("a group created this run must stay visible while empty")
	}
}

func TestPatch31_HidingEmptyGroupMovesCursorToSurvivingAncestor(t *testing.T) {
	isolatePatch31Config(t)
	home := NewHome()
	home.width, home.height = 120, 40
	home.initialLoading = false
	home.showEmptyGroups = true

	s := session.NewInstanceWithTool("s1", "/tmp/s1", "claude")
	s.GroupPath = "parent"
	home.instancesMu.Lock()
	home.instances = []*session.Instance{s}
	home.instancesMu.Unlock()
	home.groupTree = session.NewGroupTree([]*session.Instance{s})
	parent := home.groupTree.Groups["parent"]
	if parent == nil {
		t.Fatal("fixture: group 'parent' missing")
	}
	kid := home.groupTree.CreateSubgroup(parent.Path, "kid")
	home.rebuildFlatItems()

	kidIdx := -1
	for i, it := range home.flatItems {
		if it.Type == session.ItemTypeGroup && it.Path == kid.Path {
			kidIdx = i
		}
	}
	if kidIdx < 0 {
		t.Fatalf("empty subgroup %q must be visible while show_empty_groups=true", kid.Path)
	}
	home.cursor = kidIdx

	identity := home.captureSelectedItemIdentity()
	home.showEmptyGroups = false
	home.rebuildFlatItemsPreservingSelection(identity)

	for _, it := range home.flatItems {
		if it.Type == session.ItemTypeGroup && it.Path == kid.Path {
			t.Fatal("empty subgroup must be hidden after the toggle flips off")
		}
	}
	cur := home.flatItems[home.cursor]
	if cur.Type != session.ItemTypeGroup || cur.Path != parent.Path {
		t.Fatalf("cursor must fall back to the surviving parent header %q, got type=%v path=%q",
			parent.Path, cur.Type, cur.Path)
	}
}

func TestPatch31_SpaciousInsertsNonSelectableSpacersAndNavSkipsThem(t *testing.T) {
	home := p31Home(t)
	home.listDensity = session.DensitySpacious
	home.rebuildFlatItems()

	var spacers []int
	for i, it := range home.flatItems {
		if it.Type == session.ItemTypeDivider && it.Spacer {
			spacers = append(spacers, i)
		}
	}
	// Two visible top-level groups (alpha, beta) -> exactly one spacer, before
	// the second; none before the first visible row.
	if len(spacers) != 1 {
		t.Fatalf("want 1 spacer between 2 top-level groups, got %d (items=%d)", len(spacers), len(home.flatItems))
	}
	sp := spacers[0]
	if sp == 0 {
		t.Fatal("no spacer before the first visible row")
	}
	if next := home.flatItems[sp+1]; next.Type != session.ItemTypeGroup || next.Level != 0 {
		t.Fatalf("spacer must precede a top-level group header, got type=%v level=%d", next.Type, next.Level)
	}
	for _, i := range selectableItemIndices(home.flatItems) {
		if i == sp {
			t.Fatal("spacer must not be selectable")
		}
	}

	// j / k and arrow keys glide over it.
	home.cursor = sp - 1
	home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if home.cursor != sp+1 {
		t.Fatalf("j across spacer: cursor=%d, want %d", home.cursor, sp+1)
	}
	home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if home.cursor != sp-1 {
		t.Fatalf("k across spacer: cursor=%d, want %d", home.cursor, sp-1)
	}
	home.handleMainKey(tea.KeyMsg{Type: tea.KeyDown})
	if home.cursor != sp+1 {
		t.Fatalf("down across spacer: cursor=%d, want %d", home.cursor, sp+1)
	}
	home.handleMainKey(tea.KeyMsg{Type: tea.KeyUp})
	if home.cursor != sp-1 {
		t.Fatalf("up across spacer: cursor=%d, want %d", home.cursor, sp-1)
	}

	// A spacer renders as exactly one blank line.
	var b strings.Builder
	home.renderDivider(&b, home.flatItems[sp])
	if b.String() != "\n" {
		t.Fatalf("spacer must render as one blank line, got %q", b.String())
	}
}

func TestPatch31_ComfortableAndCompactInsertNoSpacers(t *testing.T) {
	home := p31Home(t)
	for _, d := range []string{"", session.DensityComfortable, session.DensityCompact} {
		home.listDensity = d
		home.rebuildFlatItems()
		for _, it := range home.flatItems {
			if it.Type == session.ItemTypeDivider {
				t.Fatalf("density %q must not insert dividers/spacers", d)
			}
		}
	}
}

func TestPatch31_SpacerUnderCursorIsSteppedOffAfterRebuild(t *testing.T) {
	home := p31Home(t)
	home.listDensity = session.DensitySpacious
	home.rebuildFlatItems()
	sp := -1
	for i, it := range home.flatItems {
		if it.Spacer {
			sp = i
		}
	}
	if sp < 0 {
		t.Fatal("expected a spacer")
	}
	home.cursor = sp
	home.rebuildFlatItems() // index-preserving rebuild
	if home.flatItems[home.cursor].Type == session.ItemTypeDivider {
		t.Fatal("cursor must not stay parked on a spacer after a rebuild")
	}
}

func TestPatch31_ApplyListDisplaySettings(t *testing.T) {
	h := &Home{}
	if h.density() != session.DensityComfortable {
		t.Fatalf("zero-value Home density = %q, want comfortable", h.density())
	}
	h.applyListDisplaySettings(session.DisplaySettings{
		Density: "SPACIOUS", ShowToolLabel: true, ShowInheritedAccount: true, ShowEmptyGroups: true,
	})
	if h.density() != session.DensitySpacious || !h.showToolLabel || !h.showInheritedAccount || !h.showEmptyGroups {
		t.Fatalf("apply failed: density=%q tool=%v inherited=%v empty=%v",
			h.density(), h.showToolLabel, h.showInheritedAccount, h.showEmptyGroups)
	}
}

package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// TestSeamB_CopyPicker_ReachableViaC drives the real Bubble Tea runtime the way
// the user does: select a session, press C, and assert the picker actually
// renders. Every other test here exercises the picker in isolation — this is
// the one that proves it is *reachable*, i.e. that the key dispatch, the modal
// guard and the overlay ordering in View all line up. An overlay checked
// earlier in the render chain would silently win and no unit test would notice.
func TestSeamB_CopyPicker_ReachableViaC(t *testing.T) {
	inst := &session.Instance{
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}

	h := seamBNewHome()
	h.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: inst}}
	h.cursor = 0

	w := &seamBTestWrapper{home: h}
	tm := teatest.NewTestModel(t, w, teatest.WithInitialTermSize(140, 50))
	tm.Send(tea.WindowSizeMsg{Width: 140, Height: 50})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})

	tm.Send(tea.QuitMsg{})
	if err := tm.Quit(); err != nil {
		t.Logf("tm.Quit: %v", err)
	}

	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(*seamBTestWrapper)
	if !final.home.copyFieldPicker.IsVisible() {
		t.Fatal("pressing C did not open the copy picker")
	}
	if !final.home.hasModalVisible() {
		t.Error("an open copy picker must register as a modal, or background ticks repaint over it")
	}

	view := final.home.View()
	for _, want := range []string{"Copy from preview", "Session ID", inst.ClaudeSessionID} {
		if !strings.Contains(view, want) {
			t.Errorf("rendered frame missing %q — another overlay is winning the View() chain:\n%s", want, view)
		}
	}
}

// TestCopyInfoHotkeyRebinds pins the dispatch contract for the `C` case.
// handleMainKey switches on defaultHotkeyBindings[hotkeyCopyInfo] ("C"), which
// only works because normalizeMainKey rewrites whatever the user actually bound
// back to that canonical key first. A reviewer flagged this as broken for
// rebinds; this test is the refutation, and it will fail loudly if the
// canonicalization ever stops covering copy_info.
func TestCopyInfoHotkeyRebinds(t *testing.T) {
	bindings := resolveHotkeys(map[string]string{hotkeyCopyInfo: "0"})
	lookup, _ := buildHotkeyLookup(bindings)

	canonical := defaultHotkeyBindings[hotkeyCopyInfo]
	if got := lookup["0"]; got != canonical {
		t.Errorf("rebound key '0' normalized to %q, want the canonical %q reached by the switch case", got, canonical)
	}

	// Unbound defaults must stop reaching the case, or the old key would keep
	// firing alongside the new one.
	_, blocked := buildHotkeyLookup(resolveHotkeys(map[string]string{hotkeyCopyInfo: ""}))
	if !blocked[canonical] {
		t.Errorf("unbinding copy_info should block %q", canonical)
	}
}

// TestRenderCopyHintLine verifies the PREVIEW pane advertises the copy keys
// next to the values they apply to. The footer alone was not enough: the
// previous binary had a working copy key that nobody could find.
func TestRenderCopyHintLine(t *testing.T) {
	h := seamBNewHome()
	h.hotkeys = resolveHotkeys(nil)

	var b strings.Builder
	h.renderCopyHintLine(&b)
	got := b.String()

	for _, want := range []string{"Copy:", "id/path/branch", "output", "pane"} {
		if !strings.Contains(got, want) {
			t.Errorf("copy hint missing %q, got %q", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("hint must end its line, got %q", got)
	}
}

// TestRenderCopyHintLine_AllUnbound verifies the line disappears rather than
// rendering a bare "Copy:" label when every copy action is unbound.
func TestRenderCopyHintLine_AllUnbound(t *testing.T) {
	h := seamBNewHome()
	h.hotkeys = resolveHotkeys(map[string]string{
		hotkeyCopyInfo:   "",
		hotkeyCopyOutput: "",
		hotkeyCopyPane:   "",
	})

	var b strings.Builder
	h.renderCopyHintLine(&b)
	if got := b.String(); got != "" {
		t.Errorf("expected no hint when every copy key is unbound, got %q", got)
	}
}

func fieldLabels(fields []copyField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.label)
	}
	return out
}

func findField(t *testing.T, fields []copyField, label string) copyField {
	t.Helper()
	for _, f := range fields {
		if f.label == label {
			return f
		}
	}
	t.Fatalf("expected a %q field, got %v", label, fieldLabels(fields))
	return copyField{}
}

// TestBuildCopyFields_SessionIDFirst is the whole point of the picker: the
// session ID shown in the PREVIEW pane must be copyable on its own, and it
// must be the first entry so it is reachable with a single keystroke.
func TestBuildCopyFields_SessionIDFirst(t *testing.T) {
	inst := &session.Instance{
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}

	fields := buildCopyFields(inst)
	if len(fields) == 0 {
		t.Fatal("expected copyable fields")
	}
	if fields[0].label != "Session ID" {
		t.Errorf("expected Session ID first, got %v", fieldLabels(fields))
	}
	if fields[0].value != "7e60e581-5c76-4523-ae62-5de8741cd18c" {
		t.Errorf("session ID value = %q, want the bare id with no label prefix", fields[0].value)
	}
}

// TestBuildCopyFields_BareValues verifies each entry copies the value alone.
// A "Path" entry that carried the "Path: " label would paste badly into a
// shell, which is the difference between this and the all-info block.
func TestBuildCopyFields_BareValues(t *testing.T) {
	inst := &session.Instance{
		Title:       "scratch",
		ProjectPath: "/tmp/scratch",
		Tool:        "claude",
	}

	path := findField(t, buildCopyFields(inst), "Path")
	if path.value != "/tmp/scratch" {
		t.Errorf("Path value = %q, want the bare path", path.value)
	}
}

// TestBuildCopyFields_DropsEmpty verifies a session with no detected ID does
// not offer an entry that would copy an empty string.
func TestBuildCopyFields_DropsEmpty(t *testing.T) {
	inst := &session.Instance{
		Title:       "scratch",
		ProjectPath: "/tmp/scratch",
		Tool:        "claude",
	}

	for _, f := range buildCopyFields(inst) {
		if strings.TrimSpace(f.value) == "" {
			t.Errorf("field %q has an empty value", f.label)
		}
		if f.label == "Session ID" {
			t.Error("session without an ID should not offer a Session ID field")
		}
	}
}

// TestBuildCopyFields_Worktree verifies worktree sessions expose Repo, Path
// and Branch as separate entries rather than one blob.
func TestBuildCopyFields_Worktree(t *testing.T) {
	inst := &session.Instance{
		Title:            "feature/x",
		ProjectPath:      "/repo/.worktrees/feature-x",
		WorktreePath:     "/repo/.worktrees/feature-x",
		WorktreeRepoRoot: "/repo",
		WorktreeBranch:   "feature/x",
	}

	fields := buildCopyFields(inst)
	if got := findField(t, fields, "Repo").value; got != "/repo" {
		t.Errorf("Repo = %q", got)
	}
	if got := findField(t, fields, "Path").value; got != "/repo/.worktrees/feature-x" {
		t.Errorf("Path = %q", got)
	}
	if got := findField(t, fields, "Branch").value; got != "feature/x" {
		t.Errorf("Branch = %q", got)
	}
}

// TestBuildCopyFields_MultiRepo verifies every project path is individually
// copyable, numbered in the order the preview lists them.
func TestBuildCopyFields_MultiRepo(t *testing.T) {
	inst := &session.Instance{
		Title:            "multi",
		ProjectPath:      "/repos/api",
		MultiRepoEnabled: true,
		AdditionalPaths:  []string{"/repos/web", "/repos/shared"},
	}

	fields := buildCopyFields(inst)
	for i, want := range []string{"/repos/api", "/repos/web", "/repos/shared"} {
		label := "Path " + string(rune('1'+i))
		if got := findField(t, fields, label).value; got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
}

// TestBuildCopyFields_KeepsAllInfoBlock pins backward compatibility: `C` used
// to copy the labelled block outright, so the block must remain reachable.
func TestBuildCopyFields_KeepsAllInfoBlock(t *testing.T) {
	inst := &session.Instance{
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}

	block := findField(t, buildCopyFields(inst), "All info (block)")
	if block.value != buildSessionInfoForCopy(inst) {
		t.Errorf("all-info entry drifted from buildSessionInfoForCopy:\n%s", block.value)
	}
}

// TestCopyFieldKeysAreStableAcrossSessionShapes is the fix for a review finding:
// with positional digits, "the full info block" was 5 on a plain session and 7
// on a worktree, so the shortcut was unlearnable. Keys are label-derived now,
// and this pins that they do not move when the entry list changes shape.
func TestCopyFieldKeysAreStableAcrossSessionShapes(t *testing.T) {
	plain := &session.Instance{
		Title:           "plain",
		ProjectPath:     "/tmp/scratch",
		Tool:            "claude",
		ClaudeSessionID: "id-1",
	}
	worktree := &session.Instance{
		Title:            "wt",
		ProjectPath:      "/repo/.worktrees/x",
		WorktreePath:     "/repo/.worktrees/x",
		WorktreeRepoRoot: "/repo",
		WorktreeBranch:   "feature/x",
		Tool:             "claude",
		ClaudeSessionID:  "id-2",
		Notes:            "some notes",
	}
	noID := &session.Instance{
		Title:       "no-id",
		ProjectPath: "/tmp/scratch",
		Tool:        "claude",
	}

	keyFor := func(inst *session.Instance, label string) string {
		for _, f := range buildCopyFields(inst) {
			if f.label == label {
				return f.key
			}
		}
		t.Fatalf("no %q field for %s", label, inst.Title)
		return ""
	}

	for _, label := range []string{"Path", "All info (block)"} {
		a, b, c := keyFor(plain, label), keyFor(worktree, label), keyFor(noID, label)
		if a != b || b != c {
			t.Errorf("%s key drifted across session shapes: plain=%q worktree=%q no-id=%q", label, a, b, c)
		}
		if a == "" {
			t.Errorf("%s should have an accelerator", label)
		}
	}

	// Dropping the session ID must not shift anyone else's key.
	if keyFor(plain, "Name") != keyFor(noID, "Name") {
		t.Error("Name key shifted when Session ID was absent")
	}
}

// TestCopyPickerAllInfoKey verifies the pre-picker behaviour is still a fixed
// two-keystroke sequence: C then a.
func TestCopyPickerAllInfoKey(t *testing.T) {
	inst := &session.Instance{
		Title:            "wt",
		ProjectPath:      "/repo/.worktrees/x",
		WorktreePath:     "/repo/.worktrees/x",
		WorktreeRepoRoot: "/repo",
		WorktreeBranch:   "feature/x",
		Tool:             "claude",
		ClaudeSessionID:  "id-2",
	}

	d := NewCopyFieldPicker()
	d.Show(inst)

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("expected 'a' to select the all-info block")
	}
	msg := cmd().(copyFieldSelectedMsg)
	if msg.value != buildSessionInfoForCopy(inst) {
		t.Errorf("'a' copied %q, want the full info block", msg.value)
	}
}

// TestCopyPickerShow_NoFields verifies the picker refuses to open on a session
// with nothing to copy, so the caller can report an error instead of showing
// an empty dialog.
func TestCopyPickerShow_NoFields(t *testing.T) {
	d := NewCopyFieldPicker()
	if d.Show(nil) {
		t.Error("expected Show(nil) to report no fields")
	}
	if d.IsVisible() {
		t.Error("picker should stay hidden when there is nothing to copy")
	}

	// A failed Show must also close an already-open picker, so the previous
	// session's entries can never be left on screen under a new selection.
	if !d.Show(&session.Instance{Title: "real", ProjectPath: "/tmp/x"}) {
		t.Fatal("expected the picker to open for a session with a path")
	}
	if d.Show(nil) || d.IsVisible() {
		t.Error("a failed Show must close the picker, not leave stale entries visible")
	}
}

// TestFormatCopyValueRespectsBudget guards the truncation arithmetic. The edge
// both reviewers flagged is a many-line value in a minimum-width dialog, where
// the " +N more" marker alone is wider than the column budget: at room=9 a
// 40-line value wants " +40 more" (9) plus a slice of the first line, which
// overflows unless the composed string is clamped.
func TestFormatCopyValueRespectsBudget(t *testing.T) {
	values := []string{
		"",
		"short",
		"/some/quite/long/project/path/that/keeps/going/and/going",
		"first line\nsecond line",
		strings.Repeat("a note line\n", 40),
		strings.Repeat("x", 200) + strings.Repeat("\n", 150),
	}

	for _, v := range values {
		for room := 1; room <= 40; room++ {
			got := formatCopyValue(v, room)
			if n := len([]rune(got)); n > room {
				t.Errorf("formatCopyValue(%d extra lines, room=%d) = %q (%d cols), over budget",
					strings.Count(v, "\n"), room, got, n)
			}
			if strings.Contains(got, "\n") {
				t.Errorf("formatCopyValue(room=%d) returned a multi-line row: %q", room, got)
			}
		}
	}
}

// TestCopyRowChromeMatchesComposedRow pins the value budget against the row
// View actually builds. A reviewer caught this off by one column, which wasted
// a character of every value; the point of the helper is that the budget and
// the composition can no longer drift apart silently.
func TestCopyRowChromeMatchesComposedRow(t *testing.T) {
	for _, labelWidth := range []int{1, 4, 10, 16, 32} {
		prefix := "> "
		quick := "a "
		label := strings.Repeat("L", labelWidth)
		row := prefix + quick + label + "  "

		if got := copyRowChrome(labelWidth); got != len(row) {
			t.Errorf("copyRowChrome(%d) = %d, but View composes %d columns before the value",
				labelWidth, got, len(row))
		}
	}
}

// TestFormatCopyValueMarksExtraLines verifies a multi-line value still tells
// the user there is more to it when there is room to say so.
func TestFormatCopyValueMarksExtraLines(t *testing.T) {
	got := formatCopyValue("Repo: /repo\nPath: /repo/x\nBranch: main", 30)
	if !strings.Contains(got, "+2 more") {
		t.Errorf("expected a +2 more marker, got %q", got)
	}
	if !strings.Contains(got, "Repo:") {
		t.Errorf("expected the first line to survive, got %q", got)
	}
}

// TestCopyPickerQuickKey verifies a digit copies the matching entry directly,
// which is what makes "grab the session ID" a two-keystroke operation.
func TestCopyPickerQuickKey(t *testing.T) {
	inst := &session.Instance{
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}

	d := NewCopyFieldPicker()
	if !d.Show(inst) {
		t.Fatal("expected the picker to open")
	}

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if cmd == nil {
		t.Fatal("expected a selection command")
	}
	msg, ok := cmd().(copyFieldSelectedMsg)
	if !ok {
		t.Fatalf("expected copyFieldSelectedMsg, got %T", cmd())
	}
	if msg.label != "Session ID" || msg.value != inst.ClaudeSessionID {
		t.Errorf("selected %q=%q, want the session ID", msg.label, msg.value)
	}
	if msg.sessionTitle != "Agent-Deck" {
		t.Errorf("sessionTitle = %q", msg.sessionTitle)
	}
	if d.IsVisible() {
		t.Error("picker should close after a selection")
	}
}

// TestCopyPickerEnterUsesCursor verifies arrow navigation + Enter picks the
// highlighted row, not always the first one.
func TestCopyPickerEnterUsesCursor(t *testing.T) {
	inst := &session.Instance{
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}

	d := NewCopyFieldPicker()
	d.Show(inst)
	want := d.fields[1]

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a selection command")
	}
	msg := cmd().(copyFieldSelectedMsg)
	if msg.label != want.label || msg.value != want.value {
		t.Errorf("selected %q, want %q", msg.label, want.label)
	}
}

// TestCopyPickerEsc verifies Esc dismisses without copying.
func TestCopyPickerEsc(t *testing.T) {
	inst := &session.Instance{
		Title:       "scratch",
		ProjectPath: "/tmp/scratch",
	}

	d := NewCopyFieldPicker()
	d.Show(inst)

	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Error("Esc should not emit a selection")
	}
	if d.IsVisible() {
		t.Error("Esc should close the picker")
	}
}

// TestCopyPickerViewShowsValues verifies the rendered dialog actually shows the
// values, so the user can tell the rows apart before copying one.
func TestCopyPickerViewShowsValues(t *testing.T) {
	inst := &session.Instance{
		Title:           "Agent-Deck",
		ProjectPath:     "/home/jtorres",
		Tool:            "claude",
		ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c",
	}

	d := NewCopyFieldPicker()
	d.SetSize(120, 40)
	d.Show(inst)

	view := d.View()
	for _, want := range []string{"Copy from preview", "Session ID", "/home/jtorres"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

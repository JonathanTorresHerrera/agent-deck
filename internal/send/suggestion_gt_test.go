package send

import (
	"strings"
	"testing"
	"time"
)

// Claude Code 2.1.296 draws its composer prompt as ">" plus a no-break space
// (not "❯"), and a named session puts the name in the top border
// ("──────── <name> ─"). The suggestion check only looked for "❯"/"›", so on
// these panes it never saw the dim body, and the guard read Claude's own prompt
// suggestion as an operator draft: every send to the session was refused as
// composer_blocked (WO-DECK-BELL-CLEAR-01, live 2026-10-12). The shapes below
// follow `tmux capture-pane -p -e` of that pane; the text is synthetic.
func gtPane(topBorder string, composerLines ...string) string {
	rule := "\x1b[38;2;136;136;136m" + strings.Repeat("─", 60) + "\x1b[39m"
	history := "\x1b[38;5;231m> an earlier submitted prompt\x1b[39m"
	return "● done 2:40 PM\n" + history + "\n\n" + topBorder + "\n" +
		strings.Join(composerLines, "\n") + "\n" + rule + "\n" +
		"  Sonnet 5 │ repo ░░░░ 14%\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"
}

var (
	gtLabelledBorder = "\x1b[38;2;136;136;136m" + strings.Repeat("─", 60) + " WO-X-v1.16.28 ─\x1b[39m"
	gtPlainBorder    = "\x1b[38;2;136;136;136m" + strings.Repeat("─", 60) + "\x1b[39m"
	gtDimSuggestion  = "\x1b[39m>\u00a0\x1b[2mdelete the scratch clone\x1b[0m"
	gtGreySuggestion = "\x1b[39m>\u00a0\x1b[90mrun the tests again\x1b[39m"
	gtRealDraft      = "\x1b[39m>\u00a0delete the scratch clone"
)

func TestComposerDraft_GtComposerSuggestionIsNotADraft(t *testing.T) {
	for name, raw := range map[string]string{
		"labelled border, dim":  gtPane(gtLabelledBorder, gtDimSuggestion),
		"plain border, dim":     gtPane(gtPlainBorder, gtDimSuggestion),
		"labelled border, grey": gtPane(gtLabelledBorder, gtGreySuggestion),
	} {
		if !ComposerBodyIsSuggestion(raw) {
			t.Errorf("%s: suggestion not detected", name)
		}
		draft, visible := ComposerDraft(raw, stripANSI)
		if !visible || draft != "" {
			t.Errorf("%s: want visible empty composer, got visible=%v draft=%q", name, visible, draft)
		}
		if ComposerHasDraft(raw, stripANSI) {
			t.Errorf("%s: a prompt suggestion must not count as a draft", name)
		}
	}
}

func TestComposerDraft_GtComposerRealDraftIsStillADraft(t *testing.T) {
	for name, raw := range map[string]string{
		"labelled border": gtPane(gtLabelledBorder, gtRealDraft),
		"plain border":    gtPane(gtPlainBorder, gtRealDraft),
		"multi-line":      gtPane(gtLabelledBorder, gtRealDraft, "  \x1b[2mand this row is dim\x1b[0m"),
	} {
		if ComposerBodyIsSuggestion(raw) {
			t.Errorf("%s: a typed draft must not read as a suggestion", name)
		}
		if !ComposerHasDraft(raw, stripANSI) {
			t.Errorf("%s: the typed draft must be guarded", name)
		}
	}
}

// A ">" outside the framed composer is quoted text or history, never the
// composer, so it cannot decide the suggestion check.
func TestComposerBodyIsSuggestion_GtOutsideTheFrameIsIgnored(t *testing.T) {
	raw := "output\n> \x1b[2mquoted dim text\x1b[0m\n"
	if ComposerBodyIsSuggestion(raw) {
		t.Fatal("a > line with no composer frame must not read as a suggestion")
	}
}

func TestGuardComposerDraft_GtComposerSuggestionPassesThrough(t *testing.T) {
	target := &fakeGuardTarget{captures: []string{gtPane(gtLabelledBorder, gtDimSuggestion)}}
	res := GuardComposerDraft(target, ComposerGuardOptions{HoldWait: time.Second, PollInterval: time.Millisecond, Strip: stripANSI})
	if res.Refused || res.SavedDraft != "" || target.ctrlCCalls != 0 || target.captureCalls != 1 {
		t.Fatalf("suggestion in a > composer must pass straight through: %+v captures=%d interrupts=%d", res, target.captureCalls, target.ctrlCCalls)
	}
	real := &fakeGuardTarget{captures: []string{gtPane(gtLabelledBorder, gtRealDraft)}}
	res = GuardComposerDraft(real, ComposerGuardOptions{HoldWait: time.Millisecond, PollInterval: time.Millisecond, Strip: stripANSI})
	if !res.Refused || res.SavedDraft != "" || real.ctrlCCalls != 0 {
		t.Fatalf("a typed draft in a > composer must be refused untouched: %+v interrupts=%d", res, real.ctrlCCalls)
	}
}

// Claude indents every draft continuation row by two spaces and draws its real
// borders at column 0, so a separator typed inside a draft is never a border.
// Before the column-0 anchor it framed the composer below the prompt row, the
// draft read as "no composer visible", and the guard let a send type over it.
func TestComposerDraft_TypedSeparatorInsideDraftIsNotABorder(t *testing.T) {
	sep := "  " + strings.Repeat("─", 20)
	for name, raw := range map[string]string{
		"plain separator":    gtPane(gtLabelledBorder, gtRealDraft, sep, "  second part"),
		"labelled separator": gtPane(gtPlainBorder, gtRealDraft, sep+" note ─", "  second part"),
	} {
		if ComposerBodyIsSuggestion(raw) {
			t.Errorf("%s: a typed draft must not read as a suggestion", name)
		}
		draft, visible := ComposerDraft(raw, stripANSI)
		if !visible || !strings.HasPrefix(draft, "delete the scratch clone") {
			t.Errorf("%s: want the whole draft guarded, got visible=%v draft=%q", name, visible, draft)
		}
	}
}

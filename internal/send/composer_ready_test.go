package send

import (
	"strings"
	"testing"
)

// readyPane renders a Claude Code 2.1.296 screen the way tmux capture-pane -e
// returns it: transcript lines, the composer between two dividers, then the
// footer (status line or picker rows).
func readyPane(transcript []string, composerBody string, footer ...string) string {
	div := strings.Repeat("─", 60)
	lines := append([]string{" ▐▛███▛█   Claude Code v2.1.296"}, transcript...)
	lines = append(lines, div, "\x1b[39m❯ "+composerBody, div)
	if len(footer) == 0 {
		footer = []string{"  [personal] ashesh@mac:/tmp/x | [Haiku 5.5] ctx:0%", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
	}
	return strings.Join(append(lines, footer...), "\n")
}

func TestClaudeComposerReady_EmptyComposerIsReady(t *testing.T) {
	r := ClaudeComposerReady(readyPane(nil, ""))
	if !r.Ready || r.Code != "" {
		t.Fatalf("empty idle composer must be ready, got %+v", r)
	}
}

func TestClaudeComposerReady_WindowsNativeASCIIPrompt(t *testing.T) {
	border := strings.Repeat("─", 60)
	frame := func(body string) string {
		return strings.Join([]string{
			"● marker saved.",
			"✻ Sautéed for 5s · done 11:27 AM",
			border + " WO-DECK-SEAT-SWITCH-01 native graceful demo ─",
			">\u00a0" + body,
			border,
			"  Opus 5.5 │ deck-native-switch-demo.Lenxdo █░░░░░░░░░ 11%",
		}, "\n")
	}
	if r := ClaudeComposerReady(frame("")); !r.Ready {
		t.Fatalf("empty ASCII composer must be ready, got %+v", r)
	}
	if r := ClaudeComposerReady(frame("operator draft")); r.Ready || r.Code != ComposerCodeDraft {
		t.Fatalf("ASCII composer with operator draft must refuse, got %+v", r)
	}
}

func TestClaudeComposerReady_GhostSuggestionCountsAsEmpty(t *testing.T) {
	// Live capture, lab/evidence/exp11-ghost-live.ansi.
	r := ClaudeComposerReady(readyPane([]string{"⏺ calc.py defines add(a, b)."}, "\x1b[2madd a subtract function\x1b[0m"))
	if !r.Ready || !r.Ghost {
		t.Fatalf("dim ghost suggestion must read as an empty composer, got %+v", r)
	}
}

func TestClaudeComposerReady_IdleHintCountsAsEmpty(t *testing.T) {
	r := ClaudeComposerReady(readyPane(nil, `Try "how do I log an error?"`))
	if !r.Ready || !r.Ghost {
		t.Fatalf("Try hint must read as an empty composer, got %+v", r)
	}
}

func TestClaudeComposerReady_OperatorDraftRefuses(t *testing.T) {
	r := ClaudeComposerReady(readyPane(nil, "half typed note to self"))
	if r.Ready || r.Code != ComposerCodeDraft {
		t.Fatalf("operator draft must refuse with %q, got %+v", ComposerCodeDraft, r)
	}
}

func TestClaudeComposerReady_BusyTurnRefuses(t *testing.T) {
	pane := readyPane([]string{"✻ Brewing… (3s · esc to interrupt)"}, "")
	r := ClaudeComposerReady(pane)
	if r.Ready || r.Code != ComposerCodeBusy {
		t.Fatalf("busy turn must refuse with %q, got %+v", ComposerCodeBusy, r)
	}
}

func TestClaudeComposerReady_BusyTextFarUpInTranscriptIsIgnored(t *testing.T) {
	transcript := []string{"❯ what does esc to interrupt mean?"}
	for i := 0; i < 20; i++ {
		transcript = append(transcript, "⏺ line of the answer")
	}
	r := ClaudeComposerReady(readyPane(transcript, ""))
	if !r.Ready {
		t.Fatalf("an old transcript mention of the busy hint must not block, got %+v", r)
	}
}

func TestClaudeComposerReady_SelectionDialogRefuses(t *testing.T) {
	// Live capture of the folder trust dialog: Enter here selects "No, exit".
	pane := strings.Join([]string{
		" Quick safety check: Is this a project you created or one you trust?",
		" ❯ No, exit",
		"   Yes, I trust this folder",
		" Enter to confirm · Esc to cancel",
	}, "\n")
	r := ClaudeComposerReady(pane)
	if r.Ready || r.Code != ComposerCodeDialog {
		t.Fatalf("selection dialog must refuse with %q, got %+v", ComposerCodeDialog, r)
	}
}

func TestClaudeComposerReady_NoComposerRefuses(t *testing.T) {
	r := ClaudeComposerReady("ashesh@mac ~ % ")
	if r.Ready || r.Code != ComposerCodeNoPrompt {
		t.Fatalf("pane without a composer must refuse with %q, got %+v", ComposerCodeNoPrompt, r)
	}
}

func TestClaudeComposerReady_BusyFoundUnderATitledTopBorder(t *testing.T) {
	// The spinner is only reachable through the composer's top border: the
	// footer below the composer (status line plus a background agent list)
	// is longer than busyLookback, so a dashes-only border match falls back
	// to the bottom lines and misses it.
	div := strings.Repeat("─", 60)
	lines := []string{"⏺ working on it", "✻ Skedaddling… (12s · ↓ 1.2k tokens)", div + " my-session ─", "❯ ", div}
	for i := 0; i < busyLookback+2; i++ {
		lines = append(lines, "  ◯ background agent "+strings.Repeat("x", i))
	}
	if r := ClaudeComposerReady(strings.Join(lines, "\n")); r.Code != ComposerCodeBusy {
		t.Fatalf("spinner above a titled top border must read as busy, got %+v", r)
	}
}

// TestClaudeComposerReady_EmptyComposerRobustToLimitBannerAndBorderedAgentPanel
// reproduces WO-DECK-SWITCH-SIDECAR-STALE-01's deck_switch_account failure:
// "Source did not reach an empty prompt; switch was not started" even though
// the pane showed an empty composer, with a weekly-limit banner above it and
// background-agent status lines below it. A bordered panel listing
// background agents, drawn below the real composer with its own divider
// pair, must never steal the "nearest two border lines" the primary parser
// picks — the real, empty composer above it must still be found.
func TestClaudeComposerReady_EmptyComposerRobustToLimitBannerAndBorderedAgentPanel(t *testing.T) {
	div := strings.Repeat("─", 60)
	lines := []string{
		" ▐▛███▛█   Claude Code v2.1.296",
		"❯ xx",
		"  ⎿  You've hit your weekly limit · resets Sep 24 at 12pm (Europe/Berlin)",
		"",
		"✻ Cooked for 0s · done 12:03 PM",
		div + " my-session ─",
		"❯ ",
		div,
		"  [personal] u@host:/Users/u | [Fable 5.1] ctx:0%",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents",
		div + " background agents ─",
	}
	// Enough agent rows to push the real composer's "❯ " line past both the
	// primary parser's (wrong) divider pair AND the 40-line fallback window,
	// matching a tall real capture rather than a short synthetic one.
	for i := 0; i < 50; i++ {
		lines = append(lines, "  ◯ background agent "+strings.Repeat("x", i)+" (running)")
	}
	lines = append(lines, div)
	r := ClaudeComposerReady(strings.Join(lines, "\n"))
	if !r.Ready {
		t.Fatalf("empty composer behind a limit banner and a bordered background-agent panel must still be ready, got %+v", r)
	}
}

func TestClaudeComposerReady_SwitchModelConfirmationIsADialog(t *testing.T) {
	// Live capture lab/evidence/exp16-switch-model-confirm.txt: no footer hint.
	pane := strings.Join([]string{
		"❯ /model claude-fable-5-1",
		strings.Repeat("─", 60),
		"  Switch model?",
		"  Your next response will be slower and use more tokens",
		"  ❯ 1. Yes, switch to Fable 5.1",
		"    2. No, go back",
	}, "\n")
	r := ClaudeComposerReady(pane)
	if r.Ready || r.Code != ComposerCodeDialog {
		t.Fatalf("numbered selection must refuse as a dialog, got %+v", r)
	}
	if sel, ok := DialogSelection(pane); !ok || sel != "Yes, switch to Fable 5.1" {
		t.Fatalf("DialogSelection = %q, %v", sel, ok)
	}
}

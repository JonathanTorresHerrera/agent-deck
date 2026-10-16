package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// WO-DECK-CODEX-LAUNCH-01. Fixtures in testdata/codex_launch:
//
//	splash-idle.txt, splash-idle-short.txt  CAPTURED 2026-10-13 from live
//	    Codex v0.161.0 panes that Agent Deck held at "running" with no turn
//	    ever started (paths anonymised). The braille-art logo has a row that
//	    begins with the spinner frame "⠙".
//	working.txt  CAPTURED the same day from a v0.161.0 pane mid-turn, trimmed
//	    to the live status line, composer and footer.
//	model-400-inferred.txt  INFERRED: the error text is captured from the
//	    rollout (task_complete.error.message); its "■" rendering follows
//	    Codex's error cell, the shape of the captured usage-limit banner.

func readCodexLaunchFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex_launch", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCodexSplashLogoIsNotBusy(t *testing.T) {
	for _, name := range []string{"splash-idle.txt", "splash-idle-short.txt"} {
		t.Run(name, func(t *testing.T) {
			content := readCodexLaunchFixture(t, name)
			// The fixture must still exercise the bug: a spinner frame leads a
			// logo row, which the generic fallback used to count as busy.
			if _, _, found := findSpinnerInContent(content, defaultSpinnerChars()); !found {
				t.Fatal("fixture no longer has a spinner-led logo row; it does not cover the regression")
			}
			s := &Session{Command: "codex"}
			if s.hasBusyIndicator(content) {
				t.Error("hasBusyIndicator = true for an idle v0.161 splash; the braille logo is not a spinner")
			}
			if !s.hasPromptIndicator(content) {
				t.Error("hasPromptIndicator = false; the idle composer must read as ready")
			}
			if s.hasErrorBannerIndicator(content) {
				t.Error("hasErrorBannerIndicator = true for a clean splash")
			}
			if got := classifyCodexSubstate(content); got != SubstateNone {
				t.Errorf("substate = %q, want none", got)
			}
		})
	}
}

func TestCodexLiveTurnStaysBusy(t *testing.T) {
	cases := map[string]string{
		"captured v0.161 working frame":             readCodexLaunchFixture(t, "working.txt"),
		"legacy braille status with interrupt hint": "⠋ Working (esc to interrupt)\n› Ask Codex to do anything\n",
		// The placeholder in the composer is drawn during a turn too; only the
		// live status line above it decides.
		"placeholder under live status": "• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.5 · /work\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			s := &Session{Command: "codex"}
			if !s.hasBusyIndicator(content) {
				t.Error("hasBusyIndicator = false for a live Codex turn")
			}
			if NewPromptDetector("codex").HasPrompt(content) {
				t.Error("HasPrompt = true while the turn runs")
			}
		})
	}
}

// Placeholder text that is not the composer (quoted in a reply above an idle
// composer, or ghost copy in prose) neither makes the pane busy nor counts as
// a second composer.
func TestCodexPlaceholderProseIsNotBusy(t *testing.T) {
	content := "• Type into the box that says Ask Codex to do anything, then press enter.\n" +
		"  ⠙ is just a glyph in this answer.\n\n" +
		"› Ask Codex to do anything\n\n  gpt-5.5 · /work\n"
	s := &Session{Command: "codex"}
	if s.hasBusyIndicator(content) {
		t.Error("hasBusyIndicator = true for prose quoting the placeholder")
	}
	if !NewPromptDetector("codex").HasPrompt(content) {
		t.Error("HasPrompt = false for an idle composer under prose")
	}
}

// A bare braille glyph still counts for tools that draw a spinner without a
// codex-style status line.
func TestBareSpinnerStillBusyForNonCodexTools(t *testing.T) {
	s := &Session{Command: "pi"}
	if !s.hasBusyIndicator("⠹ Working\n") {
		t.Error("pi spinner no longer reads busy")
	}
}

const codexModelRejectionDetail = "model gpt-5.5-codex: The 'gpt-5.5-codex' model is not supported when using Codex with a ChatGPT account."

func TestCodexModelRejectionBanner(t *testing.T) {
	content := readCodexLaunchFixture(t, "model-400-inferred.txt")
	d := NewPromptDetector("codex")
	if got := d.ClassifySubstate(content); got != SubstateModelUnsupported {
		t.Fatalf("substate = %q, want %q", got, SubstateModelUnsupported)
	}
	if got := d.SubstateDetail(content); got != codexModelRejectionDetail {
		t.Errorf("detail = %q\nwant      %q", got, codexModelRejectionDetail)
	}
	if !d.HasErrorBanner(content) {
		t.Error("HasErrorBanner = false; the rejection must route the status to error")
	}
	s := &Session{Command: "codex"}
	if s.hasBusyIndicator(content) {
		t.Error("a rejected turn is not busy")
	}
}

func TestCodexModelRejectionBannerUnwrapped(t *testing.T) {
	content := `■ {"type":"error","status":400,"error":{"type":"invalid_request_error","message":"The 'gpt-5.5-codex' model is not supported when using Codex with a ChatGPT account."}}` +
		"\n\n› Ask Codex to do anything\n\n  gpt-5.5-codex high · /work\n"
	if got := NewPromptDetector("codex").SubstateDetail(content); got != codexModelRejectionDetail {
		t.Errorf("detail = %q", got)
	}
}

func TestCodexRejectionWithoutModelKeepsMessage(t *testing.T) {
	content := `■ {"type":"error","status":400,"error":{"type":"invalid_request_error","message":"Unsupported parameter: reasoning.effort"}}` +
		"\n\n› Ask Codex to do anything\n"
	d := NewPromptDetector("codex")
	if got := d.ClassifySubstate(content); got != SubstateModelUnsupported {
		t.Fatalf("substate = %q", got)
	}
	if got := d.SubstateDetail(content); got != "Unsupported parameter: reasoning.effort" {
		t.Errorf("detail = %q", got)
	}
}

// The rejection is history once the session moved on, and quoted copies of it
// (a user prompt pasting the error, a command printing it) are never banners.
func TestCodexModelRejectionNotCurrent(t *testing.T) {
	rejection := `■ {"type":"error","status":400,"error":{"type":"invalid_request_error","message":"The 'gpt-5.5-codex' model is not supported when using Codex with a ChatGPT account."}}`
	cases := map[string]string{
		"later turn answered": rejection + "\n\n› retry with gpt-6\n\n• Done.\n\n› Ask Codex to do anything\n",
		"user prompt quotes it": `› fix this: {"type":"error","status":400,"error":{"type":"invalid_request_error"}}` +
			"\n\n• Working (2s • esc to interrupt)\n\n› Ask Codex to do anything\n",
		"tool output quotes it": "• Ran cat err.json\n  └ " + rejection + "\n\n› Ask Codex to do anything\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if got := classifyCodexSubstate(content); got == SubstateModelUnsupported {
				t.Error("history or quoted text classified as a live model rejection")
			}
		})
	}
}

func TestCodexUnsupportedModel(t *testing.T) {
	if got := CodexUnsupportedModel("The 'gpt-5.5-codex' model is not supported when using Codex with a ChatGPT account."); got != "gpt-5.5-codex" {
		t.Errorf("model = %q", got)
	}
	if got := CodexUnsupportedModel("rate limited"); got != "" {
		t.Errorf("model = %q, want empty", got)
	}
}

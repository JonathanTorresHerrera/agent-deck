package send

import "testing"

// Claude Code renders the composer as ">" (plus a no-break space when empty); the detector only knew "❯" and "›",
// so session send / launch waited for an agent that was already at its prompt (Jev rotation, 2026-10-01).
func TestCurrentComposerPrompt_AcceptsGreaterThanInsideTheComposerBlock(t *testing.T) {
	pane := "● done 2:40 PM\n\n" +
		"────────────────────────────────────────\n" +
		">  \n" +
		"────────────────────────────────────────\n" +
		"  Opus 5.5 │ vita-ehr █████████░ 95%\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"
	body, ok := CurrentComposerPrompt(pane)
	if !ok || body != "" {
		t.Fatalf("empty > composer: ok=%v body=%q", ok, body)
	}
	typed := "────────────────────────────────────────\n> hello there\n────────────────────────────────────────\n"
	body, ok = CurrentComposerPrompt(typed)
	if !ok || body != "hello there" {
		t.Fatalf("typed > composer: ok=%v body=%q", ok, body)
	}
	// Without divider lines a bare ">" is quoted text, not a composer.
	if _, ok := CurrentComposerPrompt("some output\n> quoted line\n"); ok {
		t.Fatal("divider-less > must not count as a composer")
	}
}

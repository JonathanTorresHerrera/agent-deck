package send

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// Pane shapes captured from live Codex panes (v0.158-0.161); no operator or
// agent content.
const codexUpdateModalPane = `>_ OpenAI Codex (v0.161.0)

  Update available · 0.161.0 → 0.171.0
  Release notes: https://github.com/openai/codex/releases/latest

› 1. Update now (runs ` + "`npm install -g @openai/codex`" + `)
  2. Skip
  3. Skip until next version

  enter continue · esc skip
`

const codexIdleComposerPane = `>_ OpenAI Codex (v0.161.0)
     ~/scratch
  permissions: YOLO mode

• Done.

› Ask Codex to do anything

  gpt-5.5 high · ~/scratch
  ← for agents · ? for shortcuts
`

const codexBusyPane = `>_ OpenAI Codex (v0.161.0)

• Running the synthetic task.

• Working (12s • esc to interrupt)

› Ask Codex to do anything

  gpt-5.5 high · ~/scratch
`

const codexDraftListPane = `• Done.

› 1. fix the parser

  gpt-5.5 high · ~/scratch
  ← for agents · ? for shortcuts
`

const codexApprovalPane = `• Would you like to run the following command?

  $ synthetic-command --flag

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for this command (a)
  3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to go back
`

const codexTrustPane = `> You are running Codex in /tmp/scratch

  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection.

› 1. Yes, continue
  2. No, quit

  Press enter to continue
`

func TestDetectCodexDialog(t *testing.T) {
	cases := []struct {
		name string
		pane string
		kind string
	}{
		{"update prompt", codexUpdateModalPane, CodexDialogUpdatePrompt},
		{"approval", codexApprovalPane, CodexDialogApproval},
		{"trust", codexTrustPane, CodexDialogTrust},
		{"idle composer", codexIdleComposerPane, ""},
		{"busy turn", codexBusyPane, ""},
		{"a draft that starts with a number", codexDraftListPane, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialog, ok := DetectCodexDialog(tc.pane)
			if tc.kind == "" {
				if ok {
					t.Fatalf("dialog %+v reported on a pane without one", dialog)
				}
				return
			}
			if !ok || dialog.Kind != tc.kind {
				t.Fatalf("dialog = %+v (ok=%v), want kind %q", dialog, ok, tc.kind)
			}
		})
	}
}

func TestCodexUpdatePromptIsRecognizedWithItsVersions(t *testing.T) {
	dialog, ok := IsCodexUpdatePrompt(codexUpdateModalPane)
	if !ok || dialog.From != "0.161.0" || dialog.To != "0.171.0" {
		t.Fatalf("dialog = %+v ok=%v", dialog, ok)
	}
	// Without Codex's own "esc skip" hint, Escape is not a known skip.
	noHint := strings.Replace(codexUpdateModalPane, "enter continue · esc skip", "", 1)
	if _, ok := IsCodexUpdatePrompt(noHint); ok {
		t.Fatal("update prompt without its esc hint was treated as safely skippable")
	}
	if _, ok := DetectCodexDialog(noHint); !ok {
		t.Fatal("update prompt without its hint must still block typing")
	}
}

// The hazard this file exists for: the generic Codex prompt detector reads
// the update prompt's "› 1. Update now" row as a ready composer, so a send
// that trusted it would press Enter on "Update now".
func TestGenericCodexPromptDetectorMistakesTheUpdatePromptForAComposer(t *testing.T) {
	if !tmux.NewPromptDetector("codex").HasPrompt(codexUpdateModalPane) {
		t.Skip("the generic detector no longer mistakes the update prompt; the dialog gate stays as defense in depth")
	}
	if _, ok := DetectCodexDialog(codexUpdateModalPane); !ok {
		t.Fatal("dialog gate missed the update prompt the prompt detector accepts")
	}
}

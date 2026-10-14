package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// Real pane captures, 2026-09-29 (WO-DECK-WORKTREE-TRUST-01, agent-deck-mcp
// test/prompts.test.ts), trimmed.
const claudeTrustCapture = ` Accessing workspace:
 D:\Dev_Projects\agent-deck-mcp\.worktrees\wo-deck-worktree-trust-01
 Quick safety check: Is this a project you created or one you trust? (Like your own code, a well-known open source project, or work from your team). If not,
 take a moment to review what's in this folder first.
 Claude Code'll be able to read, edit, and execute files here.
 Security guide
 ❯ No, exit
   Yes, I trust this folder
 Enter to confirm · Esc to cancel`

const codexTrustCapture = `  Folder access
  /mnt/d/Dev_Projects/agent-deck-mcp/.worktrees/trust-probe-win
  Trust this folder? Codex can read, edit, and run files here, subject to your permission settings.
› 1. Trust and continue
  2. Back to Agent Command Center
  enter continue · esc back`

func TestDetectTrustDialog_Claude(t *testing.T) {
	d, ok := DetectTrustDialog(claudeTrustCapture)
	if !ok {
		t.Fatal("Claude trust dialog not detected")
	}
	want := TrustDialog{Tool: "claude", Path: `D:\Dev_Projects\agent-deck-mcp\.worktrees\wo-deck-worktree-trust-01`, AcceptLabel: "Yes, I trust this folder"}
	if d != want {
		t.Fatalf("got %+v\nwant %+v", d, want)
	}
	if d.CursorOnAccept {
		t.Fatal("Claude's cursor starts on No, exit")
	}
}

func TestDetectTrustDialog_ClaudeCursorMovedToYes(t *testing.T) {
	moved := " Accessing workspace:\n /home/u/p\n Quick safety check: Is this a project you created or one you trust?\n   No, exit\n ❯ Yes, I trust this folder\n Enter to confirm · Esc to cancel"
	d, ok := DetectTrustDialog(moved)
	if !ok || !d.CursorOnAccept || d.Path != "/home/u/p" {
		t.Fatalf("got %+v ok=%v", d, ok)
	}
}

func TestDetectTrustDialog_CorpusClaudeCapture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "status_corpus", "claude-local-tets_8d1c8e35.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := DetectTrustDialog(string(data)); !ok || d.Tool != "claude" || d.CursorOnAccept {
		t.Fatalf("corpus trust frame: %+v ok=%v", d, ok)
	}
}

func TestDetectTrustDialog_Codex(t *testing.T) {
	d, ok := DetectTrustDialog(codexTrustCapture)
	if !ok {
		t.Fatal("Codex trust dialog not detected")
	}
	want := TrustDialog{Tool: "codex", Path: "/mnt/d/Dev_Projects/agent-deck-mcp/.worktrees/trust-probe-win", AcceptLabel: "Trust and continue", CursorOnAccept: true}
	if d != want {
		t.Fatalf("got %+v\nwant %+v", d, want)
	}
}

func TestDetectTrustDialog_NotADialog(t *testing.T) {
	cases := map[string]string{
		"idle codex splash":               readCodexLaunchFixture(t, "splash-idle.txt"),
		"prose quoting the option only":   "• Pick Yes, I trust this folder when Claude asks.\n› Ask Codex to do anything\n",
		"prose quoting the question only": "• Codex will ask: Trust this folder? Answer it.\n› Ask Codex to do anything\n",
		"empty":                           "",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if d, ok := DetectTrustDialog(content); ok {
				t.Fatalf("detected %+v", d)
			}
		})
	}
}

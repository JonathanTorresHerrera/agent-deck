package send

import (
	"regexp"
	"strings"
)

// Codex modal detection (WO-DECK-CODEX-DELIVERY-01).
//
// A Codex modal owns the keyboard: typed text answers it and the submitting
// Enter picks its highlighted option. The startup update prompt is the
// dangerous one: its default is "› 1. Update now (runs `npm install -g
// @openai/codex`)", and because it uses the composer's own "›" glyph the
// generic Codex prompt detector reads it as a ready composer. So every Codex
// send checks for a modal immediately before typing and types nothing while
// one is up.

// Codex modal kinds.
const (
	CodexDialogUpdatePrompt = "codex_update_prompt"
	CodexDialogApproval     = "codex_approval_prompt"
	CodexDialogTrust        = "trust_prompt"
	CodexDialogMenu         = "dialog"
)

// CodexDialog is one Codex modal found on the pane. It carries only Codex's
// own UI text, never operator or agent content.
type CodexDialog struct {
	Kind string
	// Line is the Codex UI line that identified the modal.
	Line string
	// From and To are the versions an update prompt offers.
	From, To string
}

var (
	codexUpdateAvailableRE = regexp.MustCompile(`Update available\s*[·•-]\s*v?([0-9][0-9A-Za-z.+-]*)\s*(?:→|->)\s*v?([0-9][0-9A-Za-z.+-]*)`)
	// A highlighted numbered option: "› 1. Update now", "❯ 2) Skip".
	codexSelectedOptionRE = regexp.MustCompile(`^\s*[›❯>]\s*\d+[.)]\s+\S`)
	// An unhighlighted numbered option under it: "  2. Skip".
	codexOtherOptionRE = regexp.MustCompile(`^\s{2,}\d+[.)]\s+\S`)
)

var codexTrustMarkers = []string{
	"do you trust the contents of this directory",
	"do you trust this folder",
	"trust this folder?",
	"allow codex to work in this folder",
}

var codexMenuFooters = []string{
	"press enter to confirm or esc to go back",
	"enter continue · esc skip",
	"enter to continue · esc to skip",
}

func codexTailLines(content string, n int) []string {
	lines := strings.Split(content, "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			tail = append(tail, strings.TrimRight(lines[i], " \t"))
		}
	}
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	return tail
}

// DetectCodexDialog reports a Codex modal in the bottom of a pane capture
// with ANSI stripped. It errs toward reporting a modal: a false positive
// only makes a send wait or refuse with nothing typed.
func DetectCodexDialog(content string) (CodexDialog, bool) {
	tail := codexTailLines(content, 20)
	joined := strings.ToLower(strings.Join(tail, "\n"))

	for _, line := range tail {
		if m := codexUpdateAvailableRE.FindStringSubmatch(line); m != nil {
			if strings.Contains(joined, "update now") || strings.Contains(joined, "skip until next version") || strings.Contains(joined, "esc skip") {
				return CodexDialog{Kind: CodexDialogUpdatePrompt, Line: strings.TrimSpace(line), From: m[1], To: m[2]}, true
			}
		}
	}
	for _, marker := range codexTrustMarkers {
		if strings.Contains(joined, marker) {
			return CodexDialog{Kind: CodexDialogTrust, Line: marker}, true
		}
	}
	if prompt := detectCodexApprovalPrompt(strings.Join(tail, "\n")); prompt != nil {
		return CodexDialog{Kind: CodexDialogApproval, Line: "approval options"}, true
	}
	selected := -1
	for i, line := range tail {
		if codexSelectedOptionRE.MatchString(line) {
			selected = i
		}
	}
	if selected >= 0 {
		for i := max(0, selected-6); i < min(len(tail), selected+7); i++ {
			if i != selected && codexOtherOptionRE.MatchString(tail[i]) {
				return CodexDialog{Kind: CodexDialogMenu, Line: "numbered selection menu"}, true
			}
		}
	}
	for _, footer := range codexMenuFooters {
		if strings.Contains(joined, footer) {
			return CodexDialog{Kind: CodexDialogMenu, Line: footer}, true
		}
	}
	return CodexDialog{}, false
}

// IsCodexUpdatePrompt reports the startup update prompt only when every part
// of it is on screen: the "Update available · A → B" header, an option list
// that offers a skip, and Codex's own "esc skip" hint. Only then is Escape a
// known "Skip" and never "Update now".
func IsCodexUpdatePrompt(content string) (CodexDialog, bool) {
	dialog, ok := DetectCodexDialog(content)
	if !ok || dialog.Kind != CodexDialogUpdatePrompt {
		return CodexDialog{}, false
	}
	lower := strings.ToLower(strings.Join(codexTailLines(content, 20), "\n"))
	if !strings.Contains(lower, "esc skip") && !strings.Contains(lower, "esc to skip") {
		return CodexDialog{}, false
	}
	if !strings.Contains(lower, "skip") {
		return CodexDialog{}, false
	}
	return dialog, true
}

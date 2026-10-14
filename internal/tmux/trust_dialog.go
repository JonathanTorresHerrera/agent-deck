package tmux

import "strings"

// TrustDialog is a first-run folder-trust prompt drawn by Claude Code or Codex.
// Captured renderings (WO-DECK-WORKTREE-TRUST-01, 2026-09-29):
//
//	Claude:  Accessing workspace:
//	         <path>
//	         Quick safety check: Is this a project you created or one you trust? ...
//	         ❯ No, exit
//	           Yes, I trust this folder
//	         Enter to confirm · Esc to cancel
//
//	Codex:   Folder access
//	         <path>
//	         Trust this folder? Codex can read, edit, and run files here, ...
//	         › 1. Trust and continue
//	           2. Back to Agent Command Center
//	         enter continue · esc back
//
// Claude's cursor starts on "No, exit", so accepting is never a bare Enter.
type TrustDialog struct {
	Tool string
	// Path is the folder the dialog names, "" when it shows none.
	Path string
	// AcceptLabel is the option that trusts the folder.
	AcceptLabel string
	// CursorOnAccept reports whether the selection cursor is on AcceptLabel.
	CursorOnAccept bool
}

type trustDialogShape struct {
	tool        string
	question    []string // any one must be on screen
	acceptLabel string
	pathHeader  string // the line above the path
	cursors     string // glyphs that mark the selected option
}

var trustDialogShapes = []trustDialogShape{
	{tool: "claude", question: []string{"one you trust?", "Do you trust the files in this folder?"},
		acceptLabel: "Yes, I trust this folder", pathHeader: "Accessing workspace:", cursors: "❯>"},
	{tool: "codex", question: []string{"Trust this folder?", "Do you trust the contents of this directory"},
		acceptLabel: "Trust and continue", pathHeader: "Folder access", cursors: "›❯>"},
}

// DetectTrustDialog reports the folder-trust dialog in the last 40 rows of
// content, if one is on screen. Both the question and the accept option must
// be present, so prose that merely quotes one of them does not match.
func DetectTrustDialog(content string) (TrustDialog, bool) {
	var lines []string
	for _, l := range lastNLines(StripANSI(content), 40) {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	for _, shape := range trustDialogShapes {
		if !containsAnyLine(lines, shape.question) {
			continue
		}
		accept := -1
		for i, l := range lines {
			if strings.Contains(l, shape.acceptLabel) {
				accept = i
			}
		}
		if accept < 0 {
			continue
		}
		d := TrustDialog{Tool: shape.tool, AcceptLabel: shape.acceptLabel}
		first, _ := firstRune(lines[accept])
		d.CursorOnAccept = strings.ContainsRune(shape.cursors, first)
		for i, l := range lines {
			if l == shape.pathHeader && i+1 < len(lines) {
				d.Path = lines[i+1]
			}
		}
		return d, true
	}
	return TrustDialog{}, false
}

func containsAnyLine(lines, subs []string) bool {
	for _, l := range lines {
		if containsAny(l, subs) {
			return true
		}
	}
	return false
}

func firstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

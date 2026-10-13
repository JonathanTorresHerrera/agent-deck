package send

import (
	"strings"
	"unicode/utf8"
)

// Claude Code renders a prompt autosuggestion (its shell-style ghost
// completion) in the composer using the SGR dim/faint attribute:
//
//	❯ <ESC>[2mrun the tests again<ESC>[0m
//
// A real operator draft carries no dim attribute:
//
//	❯ run the tests again
//
// That attribute is the ONLY thing distinguishing the two — the glyphs are
// identical once ANSI is stripped. The composer-draft guard (issue #1409)
// used to introspect stripped content only, so it classified an
// autosuggestion as an operator draft, saved it, cleared the composer, and
// then TYPED THE SUGGESTION BACK as real committable text. The next Enter
// from any source (a later send, a control-plane keystroke, the operator)
// then submitted a prompt nobody authored.
//
// Autosuggestions are accept-key semantics (Tab / Right arrow instantiate
// them), NOT placeholder semantics, so they must never be treated as content
// to preserve. The helpers below read the dim attribute off the RAW pane
// capture (tmux capture-pane -e, which CapturePaneFresh already requests)
// before it is stripped.

// composerMarkers are the prompt glyphs Claude renders at the composer.
var composerMarkers = []string{"❯", "›", ">"}

// composerMarkerBody returns the remainder of line following the composer
// marker, and whether a marker was present.
func composerMarkerBody(line string) (string, bool) {
	for _, marker := range composerMarkers {
		if idx := strings.Index(line, marker); idx >= 0 {
			return line[idx+len(marker):], true
		}
	}
	return "", false
}

// sgrState is the running rendering state a suggestion can hide behind.
// Claude (or a terminal profile) renders the ghost either with the SGR
// dim/faint attribute or in bright-black/256-colour grey — issue #1777
// documented fleets where the same suggestion arrived as `\e[90m` /
// `\e[38;5;8m` instead of `\e[2m`, defeating a dim-only check.
type sgrState struct {
	dim  bool // SGR 2 (faint)
	grey bool // bright-black foreground: SGR 90 or 38;5;8
}

// applySGR folds one SGR parameter list into the running suggestion-rendering
// state.
//
// Extended-colour introducers (38/48/58) consume their own parameters —
// `38;5;2` is colour index 2, NOT the dim attribute — so they are skipped
// explicitly. Without that, the bright-white `38;5;231` Claude uses for
// submitted messages would be misread. A 38-introduced foreground REPLACES
// the previous foreground, so it sets or clears grey; any direct foreground
// parameter (30-37, 39, 91-97) likewise clears it.
func applySGR(params string, st *sgrState) {
	if params == "" {
		// A bare CSI m is an implicit reset.
		*st = sgrState{}
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := strings.TrimSpace(parts[i])
		switch p {
		case "38", "48", "58":
			if i+1 < len(parts) {
				switch strings.TrimSpace(parts[i+1]) {
				case "5":
					if p == "38" {
						st.grey = i+2 < len(parts) && strings.TrimSpace(parts[i+2]) == "8"
					}
					i += 2 // 5;n
				case "2":
					if p == "38" {
						st.grey = false
					}
					i += 4 // 2;r;g;b
				default:
					i++
				}
			}
		case "2":
			st.dim = true
		case "0", "00":
			*st = sgrState{}
		case "22":
			st.dim = false
		case "90":
			st.grey = true
		default:
			if isForegroundColourParam(p) {
				st.grey = false
			}
		}
	}
}

// isForegroundColourParam reports whether p sets the foreground colour
// directly (classic 30-37, default 39, bright non-black 91-97).
func isForegroundColourParam(p string) bool {
	switch p {
	case "30", "31", "32", "33", "34", "35", "36", "37", "39",
		"91", "92", "93", "94", "95", "96", "97":
		return true
	}
	return false
}

// bodyStartsDim reports whether the first visible (non-whitespace) character
// in an ANSI-bearing composer body is rendered in a suggestion style — dim,
// or bright-black grey (issue #1777). Returns false when the body holds no
// visible character at all (an empty composer).
func bodyStartsDim(s string) bool {
	var st sgrState
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !isCSIFinal(s[j]) {
				j++
			}
			if j < len(s) {
				if s[j] == 'm' {
					applySGR(s[i+2:j], &st)
				}
				i = j + 1
				continue
			}
			return false
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isComposerSpace(r) {
			return st.dim || st.grey
		}
		i += size
	}
	return false
}

func isCSIFinal(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isComposerSpace covers the padding Claude puts between the marker and the
// body, including the NBSP it uses for cursor placement.
func isComposerSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == ' '
}

// ComposerBodyIsSuggestion reports whether the composer in raw (ANSI-bearing)
// pane content currently shows a dim- or grey-rendered Claude autosuggestion
// rather than operator input. The composer is read from the border-framed
// block CurrentComposerPrompt reads, where ">" (Claude Code 2.1.x) is a prompt
// marker too; a pane with no framed prompt falls back to the LAST "❯"/"›"
// marker line (earlier marker lines are submitted history).
func ComposerBodyIsSuggestion(raw string) bool {
	lines := strings.Split(raw, "\n")
	if body, rows, ok := framedComposerBody(lines); ok {
		// A suggestion is one dim prompt row; anything taller holds operator
		// input (or is ambiguous), which must stay guarded.
		return rows == 1 && bodyStartsDim(body)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		body, ok := composerMarkerBody(lines[i])
		if !ok {
			continue
		}
		return bodyStartsDim(body)
	}
	return false
}

// ComposerDraft and ComposerHasDraft (guard.go) consume ComposerBodyIsSuggestion
// directly, so every caller gets the dim check for free.

// framedComposerBody returns the raw (ANSI-bearing) text after the prompt
// marker of the composer framed by the last two border lines, the same block
// CurrentComposerPrompt parses, and how many non-blank rows the frame holds. Claude Code 2.1.296
// draws its prompt as ">" and labels a named session's top border
// ("──────── <name> ─"); before this the check knew only "❯"/"›", so a
// suggestion in such a composer read as an operator draft and every automated
// send was refused (WO-DECK-BELL-CLEAR-01, 2026-10-12).
func framedComposerBody(lines []string) (string, int, bool) {
	if len(lines) > 240 {
		lines = lines[len(lines)-240:]
	}
	bottom := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if IsComposerBorderLine(stripCSI(lines[i])) {
			bottom = i
			break
		}
	}
	top := -1
	for i := bottom - 1; i >= 0; i-- {
		if IsComposerBorderLine(stripCSI(lines[i])) {
			top = i
			break
		}
	}
	if top < 0 {
		return "", 0, false
	}
	body, found, rows := "", false, 0
	for _, line := range lines[top+1 : bottom] {
		if strings.TrimSpace(strings.ReplaceAll(stripCSI(line), " ", " ")) == "" {
			continue
		}
		rows++
		if !found {
			body, found = rawPromptBody(line)
		}
	}
	return body, rows, found
}

// rawPromptBody returns line after its prompt marker when the first visible
// character (past SGR sequences and padding) is "❯", "›" or ">".
func rawPromptBody(line string) (string, bool) {
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && !isCSIFinal(line[j]) {
				j++
			}
			if j >= len(line) {
				return "", false
			}
			i = j + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if isComposerSpace(r) {
			i += size
			continue
		}
		if r == '❯' || r == '›' || r == '>' {
			return line[i+size:], true
		}
		return "", false
	}
	return "", false
}

// stripCSI removes CSI escape sequences, enough to recognise border lines.
func stripCSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !isCSIFinal(s[j]) {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

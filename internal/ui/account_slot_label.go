package ui

import "strconv"

// Empty metadata means no explicit slot, not a claim about the running login.
func storedAccountLabel(account string) string {
	if account == "" {
		return "inherited"
	}
	return strconv.Quote(account)
}

const storedAccountPrefix = " [account:"

// Immutable presentation travels with the raw slot in the render snapshot.
// Width-independent quoting is shared across rows with the same stored slot.
type accountPresentation struct {
	label  string
	badge  string
	width  int
	quoted bool
	// Patch 38 (responsive): the compact row form, " @claude-2". Label only,
	// no brackets or quotes; the label is the strconv-escaped stored slot so
	// terminal controls cannot reach the screen. Rows use this; the long badge
	// form above stays for any other surface.
	compact      string
	compactWidth int
}

// slotsConfigured reports whether this machine has any named account slot. An
// empty stored slot only carries information when it could have been something
// else, so on a single-login machine the inherited badge is suppressed entirely
// (zero value: no badge, no width). An explicit slot always gets a presentation
// (rows may still drop the compact tag when too narrow, patch 38) — a session
// can hold a slot whose profile was since removed from config.toml, and hiding
// that would misreport which config dir the session actually runs on.
func newAccountPresentation(account string, slotsConfigured bool) accountPresentation {
	if account == "" && !slotsConfigured {
		return accountPresentation{}
	}
	label := storedAccountLabel(account)
	compactLabel := label
	if account != "" {
		// strconv.Quote output minus its surrounding quotes: still escaped.
		compactLabel = label[1 : len(label)-1]
	}
	compact := " @" + compactLabel
	return accountPresentation{
		label:        label,
		badge:        storedAccountPrefix + label + "]",
		width:        len(storedAccountPrefix) + cellWidth(label) + 1,
		quoted:       account != "",
		compact:      compact,
		compactWidth: cellWidth(compact),
	}
}

// Quote before styling/truncation so terminal controls cannot become commands.
// Keep the delimiters visible even when a long account needs an ellipsis.
func (p accountPresentation) fit(budget int) (string, int) {
	if p.width <= budget {
		return p.badge, p.width
	}
	available := budget - len(storedAccountPrefix) - 1
	if available < 1 {
		return "", 0
	}
	label := p.label
	if p.quoted {
		if available < 3 {
			return "", 0
		}
		label = "\"" + cellTruncate(label[1:len(label)-1], available-2, "…") + "\""
	} else {
		label = cellTruncate(label, available, "…")
	}
	return storedAccountPrefix + label + "]", len(storedAccountPrefix) + cellWidth(label) + 1
}

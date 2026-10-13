package ui

// Patch 51 (row seat tags). Patch 47 put a seat tag on every row, and its
// "Show default seat" toggle only hid the @claude/@codex default tags: a
// stored seat (@claude-2, @codex-3) always showed. With the PREVIEW header
// naming the seat (patch 49) the row tag is optional, so the setting is now
// a three-way choice, [display] row_seat_tags: All, Named (stored seats
// only, the pre-patch-47 behaviour) or Off. It occupies the old toggle's
// place in Settings.

import "github.com/asheshgoplani/agent-deck/internal/session"

var (
	// Named first: it is the unset default, and Space cycles Named -> All -> Off.
	rowSeatTagNames  = []string{"Named", "All", "Off"}
	rowSeatTagValues = []string{session.RowSeatTagsNamed, session.RowSeatTagsAll, session.RowSeatTagsOff}
)

func rowSeatTagIndex(v string) int {
	for i, val := range rowSeatTagValues {
		if val == v {
			return i
		}
	}
	return 0 // named
}

// rowSeatTagShown reports whether a row whose stored seat is account shows a
// seat tag. With no explicit row_seat_tags the mode follows
// showInheritedAccount, as before patch 51.
func (h *Home) rowSeatTagShown(account string) bool {
	mode := h.rowSeatTags
	if mode == "" {
		mode = session.DisplaySettings{ShowInheritedAccount: h.showInheritedAccount}.GetRowSeatTags()
	}
	switch mode {
	case session.RowSeatTagsOff:
		return false
	case session.RowSeatTagsNamed:
		return account != ""
	}
	return true
}

package ui

import "github.com/asheshgoplani/agent-deck/internal/session"

// The archive partition rule, in one place.
//
// A group's Sessions slice holds active and archived rows together, while the
// deck renders exactly one partition at a time (rebuildFlatItems keeps only
// the rows whose IsArchived matches the current view). Every surface that
// counts, lists or summarises a group therefore has to apply the same filter,
// and #1987 fixed the sidebar header by open-coding it there.
//
// The preview pane was not fixed, and drifted: reported 2026-09-17 as sidebar
// `Vita-EHR (14)` beside a preview reading `37 sessions` — 14 active rows and
// 23 archived ones in one group, each surface counting a different population
// and only one of them matching the rows on screen.
//
// Open-coding the rule a third time would invite the same drift, so both
// surfaces now share these helpers. Keep it that way: a new surface that
// touches group.Sessions directly is the bug coming back.

// renderingArchivedPartition reports whether the deck is currently showing the
// archived view (^) rather than the active one.
func (h *Home) renderingArchivedPartition() bool {
	return h.statusFilter == FilterModeArchived
}

// sessionInRenderedPartition reports whether sess is one of the rows the deck
// is currently showing.
//
// Partition-aware rather than archive-excluding: in the archived view the
// archived rows ARE the rows, so a surface must follow the partition instead
// of dropping archived sessions unconditionally.
//
// Predicate rather than slice so the per-render stats loop over every group
// stays allocation-free; use visibleGroupSessions where a slice is wanted.
func (h *Home) sessionInRenderedPartition(sess *session.Instance) bool {
	return sess != nil && sess.IsArchived() == h.renderingArchivedPartition()
}

// visibleGroupSessions returns the rows of g that belong to the partition the
// deck is currently rendering, in their existing order.
//
// Returns an empty (non-nil) slice for a group with no rows in this partition
// — callers render that as "no sessions", which is the truth for the view.
func (h *Home) visibleGroupSessions(g *session.Group) []*session.Instance {
	if g == nil {
		return nil
	}
	out := make([]*session.Instance, 0, len(g.Sessions))
	for _, sess := range g.Sessions {
		if !h.sessionInRenderedPartition(sess) {
			continue
		}
		out = append(out, sess)
	}
	return out
}

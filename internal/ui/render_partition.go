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

// Patch 55 (archive scope): ^ now cycles three scopes, Current (active rows,
// the default), Archived (archived rows only, still represented by
// statusFilter == FilterModeArchived) and All (both partitions in one list,
// h.archiveScopeAll). In the All scope "the rendered partition" is both, so
// archivedInRenderedPartition returns true for every row; the helpers below
// stay the one place that rule lives.

// archiveScope is which archive partition(s) the list shows.
type archiveScope int

const (
	archiveScopeCurrent  archiveScope = iota // active sessions only (default)
	archiveScopeArchived                     // archived sessions only (^ view)
	archiveScopeAll                          // active and archived together
)

// archiveScopeNames are the scope control's words, in cycle order.
var archiveScopeNames = [...]string{"current", "archived", "all"}

// archiveScope reports the scope the deck is rendering. The archived status
// filter wins over a stale archiveScopeAll flag, so the two can never both
// apply (code and tests set statusFilter = FilterModeArchived directly).
func (h *Home) archiveScope() archiveScope {
	switch {
	case h.statusFilter == FilterModeArchived:
		return archiveScopeArchived
	case h.archiveScopeAll:
		return archiveScopeAll
	}
	return archiveScopeCurrent
}

// showingAllArchiveScope reports whether the list mixes active and archived rows.
func (h *Home) showingAllArchiveScope() bool {
	return h.archiveScope() == archiveScopeAll
}

// setArchiveScope switches scope without rebuilding the list. Entering the
// Archived scope clears the All flag; entering All drops the archived filter.
// Leaving Archived for Current or All clears the status filter, exactly like
// pressing ^ again used to.
func (h *Home) setArchiveScope(s archiveScope) {
	switch s {
	case archiveScopeArchived:
		h.archiveScopeAll = false
		h.statusFilter = FilterModeArchived
	case archiveScopeAll:
		h.archiveScopeAll = true
		if h.statusFilter == FilterModeArchived {
			h.statusFilter = ""
		}
	default:
		h.archiveScopeAll = false
		if h.statusFilter == FilterModeArchived {
			h.statusFilter = ""
		}
	}
}

// selectArchiveScope switches scope and rebuilds the list, keeping the
// selected row when it is still visible (same contract as the other filter
// changes). Chip toggles are cleared like the keyboard filter keys do.
func (h *Home) selectArchiveScope(s archiveScope) {
	selectedBefore := h.captureSelectedItemIdentity()
	h.clearHiddenStatuses()
	h.keepEmptyFilter = true
	h.setArchiveScope(s)
	h.rebuildFlatItemsPreservingSelection(selectedBefore)
}

// cycleArchiveScope is ^ / shift+6: Current -> Archived -> All -> Current.
func (h *Home) cycleArchiveScope() {
	h.selectArchiveScope((h.archiveScope() + 1) % 3)
}

// scopeStatus is the status a row counts and filters as. In the All scope an
// archived row is "stopped": its stored status froze when it was archived
// (the status loop skips archived sessions) and the row draws the stopped
// glyph, so counting, filtering or hiding it by that stale status would
// disagree with what is on screen. Other scopes keep the stored status.
func (h *Home) scopeStatus(archived bool, status session.Status) session.Status {
	if archived && h.showingAllArchiveScope() {
		return session.StatusStopped
	}
	return status
}

// renderingArchivedPartition reports whether the deck is currently showing the
// archived view (^) rather than the active one. False in the All scope; use
// archivedInRenderedPartition to ask whether a row is on screen.
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
	return sess != nil && h.archivedInRenderedPartition(sess.IsArchived())
}

// archivedInRenderedPartition is the same rule for a caller that only has the
// archived flag (the render snapshot, remote rows) rather than an Instance.
// Patch 38 (responsive): the header counts and filter pills use it, so they
// follow the partition exactly like the list, group counts and preview do.
func (h *Home) archivedInRenderedPartition(archived bool) bool {
	if h.showingAllArchiveScope() {
		return true // Patch 55: both partitions are on screen
	}
	return archived == h.renderingArchivedPartition()
}

// groupActivityForScope is GroupTree.GroupActivityMap for the current scope.
// In the All scope a group counts as populated when it holds rows from either
// partition, but only active rows can make it "active" (archived rows are
// stopped, see scopeStatus).
func (h *Home) groupActivityForScope() map[string]session.GroupActivity {
	if !h.showingAllArchiveScope() {
		return h.groupTree.GroupActivityMap(h.renderingArchivedPartition())
	}
	merged := h.groupTree.GroupActivityMap(false)
	for path, a := range h.groupTree.GroupActivityMap(true) {
		m := merged[path]
		m.HasAny = m.HasAny || a.HasAny
		merged[path] = m
	}
	return merged
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

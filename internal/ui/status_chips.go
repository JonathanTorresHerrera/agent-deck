package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 28 (status chips): the header's status chips are clickable.
//
// Clicking a status chip (filter bar row, or the per-status segment of the
// summary line) toggles that status OFF: its sessions disappear from the list.
// Clicking again brings them back; several can be off at once. Clicking "All"
// clears every toggle and any single-status filter. The toggle set is
// independent of statusFilter: the keyboard filter keys (! @ # & 0 % ^) keep
// their single-status behaviour and clear the toggle set so the two never
// fight. Toggles are session-local UI state and are not persisted.

const (
	// Rows the clickable chips live on: View writes the header bar first (row
	// 0) and the filter bar directly under it (row 1). getListContentStartY
	// assumes the same two header rows.
	statusChipSummaryY   = 0
	statusChipFilterBarY = 1
)

// statusChipHitbox is one clickable chip: the half-open column range
// [x0, x1) on row y, as actually rendered (measured with lipgloss.Width).
type statusChipHitbox struct {
	status session.Status // which status it toggles; ignored when all is set
	all    bool           // the "All" chip: reset every toggle and filter
	x0, x1 int
	y      int
}

// statusHidden reports whether status was toggled off via its chip.
func (h *Home) statusHidden(s session.Status) bool {
	return h.hiddenStatuses[s]
}

// clearHiddenStatuses empties the toggle set without rebuilding the list;
// callers that already rebuild (the keyboard filter keys) use it directly.
func (h *Home) clearHiddenStatuses() {
	for s := range h.hiddenStatuses {
		delete(h.hiddenStatuses, s)
	}
}

// toggleHiddenStatus flips one status chip and rebuilds the list. A single
// status filter (but not the archived view) is dropped first: a chip click
// means "show everything except what I turned off".
func (h *Home) toggleHiddenStatus(s session.Status) {
	if h.statusFilter != "" && h.statusFilter != FilterModeArchived {
		h.statusFilter = ""
	}
	if h.hiddenStatuses == nil {
		h.hiddenStatuses = make(map[session.Status]bool)
	}
	if h.hiddenStatuses[s] {
		delete(h.hiddenStatuses, s)
	} else {
		h.hiddenStatuses[s] = true
	}
	h.rebuildFlatItems()
}

// resetStatusChips is the "All" chip: clear every toggle and the status filter.
func (h *Home) resetStatusChips() {
	h.clearHiddenStatuses()
	h.statusFilter = ""
	h.rebuildFlatItems()
}

// applyHiddenStatuses drops sessions whose status is toggled off, plus every
// group header left with no remaining session (same pruning as statusFilter).
// Group membership comes from the full group tree so a collapsed group that
// still holds a visible session keeps its header.
func (h *Home) applyHiddenStatuses(items []session.Item) []session.Item {
	groupsWithMatches := make(map[string]bool)
	if h.groupTree != nil {
		for _, group := range h.groupTree.GroupList {
			if group == nil {
				continue
			}
			for _, inst := range group.Sessions {
				if inst != nil && !inst.IsArchived() && !h.statusHidden(inst.Status) {
					markGroupPathAndAncestors(groupsWithMatches, group.Path)
				}
			}
		}
	}
	out := make([]session.Item, 0, len(items))
	for _, item := range items {
		switch {
		case item.Type == session.ItemTypeGroup:
			if groupsWithMatches[item.Path] {
				out = append(out, item)
			}
		case item.Type == session.ItemTypeSession && item.Session != nil:
			if !h.statusHidden(item.Session.Status) {
				out = append(out, item)
			}
		default:
			out = append(out, item)
		}
	}
	return out
}

// handleStatusChipClick maps a left press to a chip recorded by the last
// render and applies it. It reports whether the press hit a chip; clicks on
// the "!@#& filter" hint text or the gaps between chips return false.
func (h *Home) handleStatusChipClick(msg tea.MouseMsg) bool {
	for _, c := range h.chipHitboxes {
		if msg.Y != c.y || msg.X < c.x0 || msg.X >= c.x1 {
			continue
		}
		if c.all {
			h.resetStatusChips()
		} else {
			h.toggleHiddenStatus(c.status)
		}
		return true
	}
	return false
}

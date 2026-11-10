package ui

// Patch 55 (archive scope).
//
// ^ used to toggle an archived-ONLY view. It now cycles three scopes, Current
// (active sessions, the default), Archived (the old ^ view) and All (active
// and archived sessions in one list). The filter bar's "^ archived" hint is
// replaced by a scope control, "^ current·archived·all", with the active scope
// highlighted; each word is a click target that selects that scope.
//
// The partition rule itself lives in render_partition.go (archiveScope,
// archivedInRenderedPartition, scopeStatus). In the All scope archived rows
// carry a dim " [archived]" badge so they read differently from stopped
// active sessions, and they count, filter and hide as stopped.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// archiveScopeWordSep separates the scope control's words.
const archiveScopeWordSep = "·"

// archiveScopeControlPrefix is the key and space before the first word.
func archiveScopeControlPrefix() string { return FilterKeyArchived + " " }

// renderArchiveScopeControl renders "^ current·archived·all" with the active
// scope (and the key, when the scope is not the default) highlighted in the
// same subtle shade the other filter-bar hints use for an engaged filter.
func (h *Home) renderArchiveScopeControl() string {
	dim := lipgloss.NewStyle().Foreground(ColorComment).Faint(true)
	hi := lipgloss.NewStyle().Foreground(ColorTextDim)
	mark := func(c string, on bool) string {
		if on {
			return hi.Render(c)
		}
		return dim.Render(c)
	}
	scope := h.archiveScope()
	words := make([]string, len(archiveScopeNames))
	for i, name := range archiveScopeNames {
		words[i] = mark(name, archiveScope(i) == scope)
	}
	return mark(FilterKeyArchived, scope != archiveScopeCurrent) + dim.Render(" ") +
		strings.Join(words, dim.Render(archiveScopeWordSep))
}

// recordArchiveScopeHitboxes records one click target per scope word of the
// rendered filter-bar hint. x0 is the column where the hint (its lead-in)
// starts. Nothing is recorded when the fitted hint dropped the control.
func (h *Home) recordArchiveScopeHitboxes(f fittedFilterBarHint, x0 int) {
	if f.scopeIdx < 0 || f.scopeIdx >= f.kept || f.scopeIdx >= len(f.segs) {
		return
	}
	x := x0 + cellWidth(filterBarHintLeadIn)
	for i := 0; i < f.scopeIdx; i++ {
		x += cellWidth(f.segs[i]) + cellWidth(filterBarHintSep)
	}
	x += cellWidth(archiveScopeControlPrefix())
	for i, name := range archiveScopeNames {
		w := cellWidth(name)
		x1 := x + w
		if h.width > 0 && x1 > h.width {
			x1 = h.width
		}
		if x1 > x {
			h.chipHitboxes = append(h.chipHitboxes, statusChipHitbox{
				scopeTarget: true, scope: archiveScope(i), x0: x, x1: x1, y: statusChipFilterBarY,
			})
		}
		x += w + cellWidth(archiveScopeWordSep)
	}
}

// archivedRowBadge is the dim " [archived]" marker an archived row carries in
// the All scope (and nowhere else: in the Archived scope every row is one).
func (h *Home) archivedRowBadge(archived, selected bool) string {
	if !archived || !h.showingAllArchiveScope() {
		return ""
	}
	style := DimStyle
	if selected {
		style = SessionStatusSelStyle
	}
	return style.Render(" [archived]")
}

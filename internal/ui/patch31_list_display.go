package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 31 (settings density): presentation settings for the left SESSIONS
// list — density, tool-name label, inherited-account tag, empty groups. The
// config lives in [display] (see session.DisplaySettings); the Settings panel
// edits it and (*Home).applyListDisplaySettings caches it for the render path.

// applyListDisplaySettings caches the list-presentation settings from config.
// Called at startup (NewHome) and after the settings panel saves.
func (h *Home) applyListDisplaySettings(d session.DisplaySettings) {
	h.listDensity = d.GetDensity()
	h.showToolLabel = d.ShowToolLabel
	h.showInheritedAccount = d.ShowInheritedAccount
	h.showEmptyGroups = d.ShowEmptyGroups
}

// density returns the resolved list density. A zero-value Home (tests, or a
// Home built before config loaded) is comfortable — the historical rendering.
func (h *Home) density() string {
	return session.DisplaySettings{Density: h.listDensity}.GetDensity()
}

// humanizeSinceCompact is the compact-density age: largest unit only, no "ago"
// ("2h" rather than "2h 6m ago"). Units and floor math match humanizeSince.
func humanizeSinceCompact(d time.Duration) string {
	const (
		minute = time.Minute
		hour   = time.Hour
		day    = 24 * hour
		week   = 7 * day
		month  = 30 * day
		year   = 365 * day
	)
	switch {
	case d < minute: // includes negative (future/clock skew)
		return "now"
	case d < hour:
		return fmt.Sprintf("%dm", int(d/minute))
	case d < day:
		return fmt.Sprintf("%dh", int(d/hour))
	case d < week:
		return fmt.Sprintf("%dd", int(d/day))
	case d < month:
		return fmt.Sprintf("%dw", int(d/week))
	case d < year:
		return fmt.Sprintf("%dmo", int(d/month))
	default:
		return fmt.Sprintf("%dy", int(d/year))
	}
}

// formatRelativeTimeDensity renders a session-row age for the given density:
// the shared two-component formatter normally, the one-unit form for compact.
func formatRelativeTimeDensity(t time.Time, density string) string {
	if density == session.DensityCompact {
		if t.IsZero() {
			return "unknown"
		}
		return humanizeSinceCompact(time.Since(t))
	}
	return formatRelativeTime(t)
}

// hideEmptyGroups drops group headers whose recursive, partition-aware session
// count is zero — the same rule buildGroupRenderStats uses for the "(N)" in the
// header, so a header is hidden exactly when it would have read "(0)". Only the
// rendered list is affected: groupTree still holds every group, so n / N / g
// targeting by path is unchanged.
//
// A group that still has a worktree-creation placeholder pending (or an
// ancestor of one) is kept so the placeholder has a header to nest under.
func (h *Home) hideEmptyGroups(items []session.Item) []session.Item {
	if h.showEmptyGroups || h.groupTree == nil {
		return items
	}
	stats := h.buildGroupRenderStats(nil)

	keep := make(map[string]bool)
	// Groups created in this TUI run stay visible while empty (otherwise a
	// group made with `g` would vanish the moment the dialog closed).
	for p := range h.keptEmptyGroups {
		keep[p] = true
	}
	for _, creating := range h.creatingSessions {
		if creating == nil {
			continue
		}
		for p := creating.GroupPath; p != ""; {
			keep[p] = true
			idx := strings.LastIndex(p, "/")
			if idx < 0 {
				break
			}
			p = p[:idx]
		}
	}

	out := make([]session.Item, 0, len(items))
	for _, item := range items {
		if item.Type == session.ItemTypeGroup {
			path := item.Path
			if path == "" && item.Group != nil {
				path = item.Group.Path
			}
			if stats[path].sessionCount == 0 && !keep[path] {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// keepEmptyGroup marks a group (and its ancestors) as visible-while-empty for
// the rest of this TUI run. Used for groups the user just created.
func (h *Home) keepEmptyGroup(path string) {
	if h.keptEmptyGroups == nil {
		h.keptEmptyGroups = make(map[string]bool)
	}
	for p := path; p != ""; {
		h.keptEmptyGroups[p] = true
		idx := strings.LastIndex(p, "/")
		if idx < 0 {
			break
		}
		p = p[:idx]
	}
}

// isTopLevelHeader reports whether item is a root group header (local Level-0
// group, or a remote host root).
func isTopLevelHeader(item session.Item) bool {
	switch item.Type {
	case session.ItemTypeGroup:
		return item.Level == 0
	case session.ItemTypeRemoteGroup:
		return remoteGroupParentPath(item.Path) == ""
	}
	return false
}

// insertDensitySpacers inserts one blank, non-selectable spacer row before each
// top-level group header except the first visible row. Spacers are
// ItemTypeDivider rows (Spacer=true), so selectableItemIndices, skipDivider,
// mouseYToItemIndex and the one-line-per-item viewport math treat them exactly
// like the view-mode divider. No spacer is added directly after another
// divider (the view-mode divider already separates the sections).
func insertDensitySpacers(items []session.Item) []session.Item {
	out := make([]session.Item, 0, len(items)+8)
	for _, item := range items {
		if isTopLevelHeader(item) && len(out) > 0 && out[len(out)-1].Type != session.ItemTypeDivider {
			out = append(out, session.Item{Type: session.ItemTypeDivider, Spacer: true})
		}
		out = append(out, item)
	}
	return out
}

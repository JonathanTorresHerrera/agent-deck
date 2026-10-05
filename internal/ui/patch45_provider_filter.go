package ui

// Patch 45 (provider filter + badge).
//
// 1. The provider counts on the filter bar (patch 39) are click targets: a
//    click shows only that provider's sessions, a second click clears it, and
//    the "All" chip clears it with everything else. It stacks with the status
//    pills and chips, so "Grok, waiting only" is two clicks.
// 2. [display] show_provider_badge puts one provider-coloured letter right
//    after each row's status glyph. The counts on the filter bar name each
//    provider next to its letter, so they double as the legend. The deck's
//    own tool icons are emoji, which Windows Terminal draws two cells wide
//    and unevenly; a letter keeps every row aligned.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// providerKey is the provider a tool name belongs to (the tool name itself;
// a session without one is a shell).
func providerKey(tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return "shell"
	}
	return tool
}

// providerDisplayName is the name the filter bar shows for a provider.
func providerDisplayName(key string) string {
	switch key {
	case "claude":
		return "Claude"
	case "codex":
		return "ChatGPT"
	case "grok":
		return "Grok"
	case "gemini":
		return "Gemini"
	case "opencode":
		return "OpenCode"
	}
	return key
}

// providerBadgeLetter is the one-cell badge for a provider.
func providerBadgeLetter(key string) string {
	switch key {
	case "claude":
		return "C"
	case "codex":
		return "G"
	case "grok":
		return "X"
	case "gemini":
		return "*"
	case "shell":
		return "$"
	}
	r := []rune(key)
	if len(r) == 0 {
		return "?"
	}
	return strings.ToLower(string(r[0]))
}

// providerBadgeColor is the badge colour: the tool's brand colour, except
// Grok, which has none of its own and reads best plain.
func providerBadgeColor(key string) lipgloss.Color {
	if key == "grok" {
		return ColorText
	}
	return ToolColor(key)
}

// renderProviderBadge renders a row's badge; on the selected row it takes the
// selection colours like the status glyph beside it.
func renderProviderBadge(tool string, selected bool) string {
	key := providerKey(tool)
	if selected {
		return SessionStatusSelStyle.Bold(true).Render(providerBadgeLetter(key))
	}
	return lipgloss.NewStyle().Foreground(providerBadgeColor(key)).Bold(true).Render(providerBadgeLetter(key))
}

// providerSegments renders one filter-bar segment per provider at a detail
// level (0 = with the running figure, 1 = without). The selected provider is
// drawn as an active pill.
func providerSegments(counts []providerCount, level int, selected string) []string {
	nameStyle := lipgloss.NewStyle().Foreground(ColorText)
	numStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	runStyle := lipgloss.NewStyle().Foreground(ColorGreen)
	activeStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	segs := make([]string, 0, len(counts))
	for _, pc := range counts {
		key := providerKey(pc.tool)
		letter := providerBadgeLetter(key)
		name := providerDisplayName(key)
		if key == selected {
			text := fmt.Sprintf("%s %s %d", letter, name, pc.live)
			if level == 0 && pc.running > 0 {
				text += fmt.Sprintf(" (%d▶)", pc.running)
			}
			segs = append(segs, activeStyle.Render(text))
			continue
		}
		badge := lipgloss.NewStyle().Foreground(providerBadgeColor(key)).Bold(true).Render(letter)
		p := badge + " " + nameStyle.Render(name) + " " + numStyle.Render(fmt.Sprintf("%d", pc.live))
		if level == 0 && pc.running > 0 {
			p += " " + runStyle.Render(fmt.Sprintf("(%d▶)", pc.running))
		}
		segs = append(segs, p)
	}
	return segs
}

const providerSegmentLead = "   "

var providerSegmentSep = " · "

// renderProviderCountsSel is renderProviderCounts with the selected provider
// highlighted. level 2, or no providers, renders nothing.
func renderProviderCountsSel(counts []providerCount, level int, selected string) string {
	if level >= 2 || len(counts) == 0 {
		return ""
	}
	sepStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	return providerSegmentLead + strings.Join(providerSegments(counts, level, selected), sepStyle.Render(providerSegmentSep))
}

// providerCountsForBar is providerCounts plus the filtered provider when it
// has no live session (so its highlighted pill stays clickable to clear it).
func (h *Home) providerCountsForBar() []providerCount {
	counts := h.providerCounts()
	if h.providerFilter == "" {
		return counts
	}
	for _, pc := range counts {
		if providerKey(pc.tool) == h.providerFilter {
			return counts
		}
	}
	return append(counts, providerCount{tool: h.providerFilter})
}

// recordProviderHitboxes records a click target per provider segment of the
// rendered filter bar. x0 is the column where the provider block starts.
func (h *Home) recordProviderHitboxes(counts []providerCount, x0 int, rendered string) {
	if rendered == "" {
		return
	}
	level := 1
	if rendered == renderProviderCountsSel(counts, 0, h.providerFilter) {
		level = 0
	}
	x := x0 + cellWidth(providerSegmentLead)
	for i, seg := range providerSegments(counts, level, h.providerFilter) {
		w := cellWidth(seg)
		x1 := x + w
		if h.width > 0 && x1 > h.width {
			x1 = h.width
		}
		if x1 > x {
			h.chipHitboxes = append(h.chipHitboxes, statusChipHitbox{
				provider: providerKey(counts[i].tool), x0: x, x1: x1, y: statusChipFilterBarY,
			})
		}
		x += w + cellWidth(providerSegmentSep)
	}
}

// toggleProviderFilter filters the list to one provider, or clears the filter
// when that provider is already selected.
func (h *Home) toggleProviderFilter(key string) {
	selectedBefore := h.captureSelectedItemIdentity()
	if h.providerFilter == key {
		h.providerFilter = ""
	} else {
		h.providerFilter = key
	}
	h.rebuildFlatItemsPreservingSelection(selectedBefore)
}

// applyProviderFilter keeps only the filtered provider's sessions, plus the
// group headers that still hold one of them. Group membership comes from the
// full group tree (as in applyHiddenStatuses), so a collapsed group that holds
// a match keeps its header.
func (h *Home) applyProviderFilter(items []session.Item, viewArchived bool) []session.Item {
	match := func(inst *session.Instance) bool {
		return inst != nil && providerKey(inst.Tool) == h.providerFilter
	}
	groupsWithMatches := make(map[string]bool)
	if h.groupTree != nil {
		for _, group := range h.groupTree.GroupList {
			if group == nil {
				continue
			}
			for _, inst := range group.Sessions {
				if match(inst) && inst.IsArchived() == viewArchived {
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
		case item.Type == session.ItemTypeSession:
			if match(item.Session) {
				out = append(out, item)
			}
		default:
			out = append(out, item)
		}
	}
	return out
}

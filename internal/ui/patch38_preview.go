package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 38 (responsive): collapsible preview sections.
//
// The session preview is a stack of sections, each under a divider: the tool
// section (Claude / Gemini / Codex / ... all share the key "agent"),
// "Analytics", "Notes" and "Output". A left click on a divider row inside the
// preview pane collapses the section to just its divider (the space goes to
// the sections below, chiefly Output) or expands it again. The state is GLOBAL,
// not per session, and persists as [preview] collapsed_sections in config.toml.
//
// Hit-testing follows patch 28's chip hitboxes: renderPreviewPane records the
// row of every divider it actually drew (relative to the top of the preview
// content) and the layout that drew the pane records where that content sits
// on screen, so the click handler works from what is really on screen — a
// divider that was cut off by the pane height is not clickable.

const (
	previewSectionAgent     = "agent"
	previewSectionAnalytics = "analytics"
	previewSectionNotes     = "notes"
	previewSectionOutput    = "output"
)

// previewSectionKeys is the canonical order, used when persisting.
var previewSectionKeys = []string{
	previewSectionAgent, previewSectionAnalytics, previewSectionNotes, previewSectionOutput,
}

// previewSectionHit is one divider row drawn by the last renderPreviewPane.
type previewSectionHit struct {
	key string
	row int // relative to the top of the preview content
}

// loadCollapsedSections seeds the global collapsed set from config. Unknown
// keys are ignored.
func (h *Home) loadCollapsedSections(p session.PreviewSettings) {
	h.collapsedSections = make(map[string]bool, len(p.CollapsedSections))
	for _, k := range p.CollapsedSections {
		for _, known := range previewSectionKeys {
			if k == known {
				h.collapsedSections[k] = true
			}
		}
	}
}

func (h *Home) previewSectionCollapsed(key string) bool {
	return h.collapsedSections[key]
}

// collapsedSectionList returns the collapsed keys in canonical order.
func (h *Home) collapsedSectionList() []string {
	var out []string
	for _, k := range previewSectionKeys {
		if h.collapsedSections[k] {
			out = append(out, k)
		}
	}
	return out
}

// persistCollapsedSections writes the collapsed set to config.toml. It goes
// through LoadUserConfig/SaveUserConfig like persistPreviewPct, and the
// settings-panel merge (MergePanelConfigOntoDisk) starts from the on-disk
// config and never overlays this field, so a panel save keeps it.
func (h *Home) persistCollapsedSections() {
	cfg, err := session.LoadUserConfig()
	if err != nil || cfg == nil {
		return
	}
	cfg.Preview.CollapsedSections = h.collapsedSectionList()
	_ = session.SaveUserConfig(cfg)
}

// togglePreviewSection flips one section's collapsed state and persists it.
func (h *Home) togglePreviewSection(key string) {
	if h.collapsedSections == nil {
		h.collapsedSections = make(map[string]bool)
	}
	if h.collapsedSections[key] {
		delete(h.collapsedSections, key)
	} else {
		h.collapsedSections[key] = true
	}
	h.persistCollapsedSections()
}

// renderCollapsibleDivider is renderSectionDivider with a fold marker: "▾ Label"
// when expanded, "▸ Label" when collapsed. Same line style; widths are measured
// in cells (the marker is multi-byte).
func renderCollapsibleDivider(label string, width int, collapsed bool) string {
	marker := "▾"
	if collapsed {
		marker = "▸"
	}
	lineStyle := lipgloss.NewStyle().Foreground(ColorBorder)
	labelStyle := lipgloss.NewStyle().Foreground(ColorText).Bold(true)

	text := marker + " " + label
	side := (width - (cellWidth(text) + 2)) / 2
	if side < 3 {
		side = 3
	}
	return lineStyle.Render(strings.Repeat("─", side)) +
		" " + labelStyle.Render(text) + " " +
		lineStyle.Render(strings.Repeat("─", side))
}

// recordPreviewSection remembers that a divider for key was drawn at row.
func (h *Home) recordPreviewSection(key string, row int) {
	h.previewSectionHits = append(h.previewSectionHits, previewSectionHit{key: key, row: row})
}

// writePreviewSectionHeader records the divider's row, writes the divider
// line and a newline, and returns the divider line.
func (h *Home) writePreviewSectionHeader(b *strings.Builder, key, label string, width int) string {
	h.recordPreviewSection(key, strings.Count(b.String(), "\n"))
	line := renderCollapsibleDivider(label, width, h.previewSectionCollapsed(key))
	b.WriteString(line)
	b.WriteString("\n")
	return line
}

// previewSectionAt maps a screen cell to the preview section divider drawn
// there by the last render.
func (h *Home) previewSectionAt(x, y int) (string, bool) {
	if h.previewPaneRows <= 0 || x < h.previewPaneX {
		return "", false
	}
	rel := y - h.getListContentStartY() - h.previewPaneDY
	if rel < 0 || rel >= h.previewPaneRows {
		return "", false
	}
	for _, hit := range h.previewSectionHits {
		if hit.row == rel {
			return hit.key, true
		}
	}
	return "", false
}

// previewSectionScreenY returns the absolute screen row of a drawn, visible
// section divider (used by tests to aim a click).
func (h *Home) previewSectionScreenY(key string) (int, bool) {
	for _, hit := range h.previewSectionHits {
		if hit.key == key && hit.row < h.previewPaneRows {
			return h.getListContentStartY() + h.previewPaneDY + hit.row, true
		}
	}
	return 0, false
}

// handlePreviewSectionClick toggles the section whose divider was clicked. It
// reports whether the press landed on a divider.
func (h *Home) handlePreviewSectionClick(msg tea.MouseMsg) bool {
	key, ok := h.previewSectionAt(msg.X, msg.Y)
	if !ok {
		return false
	}
	h.togglePreviewSection(key)
	return true
}

// stripAidaSuffix removes a trailing " (Aida)" (exact, case-sensitive, after
// trimming trailing spaces) from a title for display. A title that is only the
// suffix is left alone.
func stripAidaSuffix(title string) string {
	t := strings.TrimRight(title, " ")
	if !strings.HasSuffix(t, " (Aida)") {
		return title
	}
	rest := strings.TrimRight(strings.TrimSuffix(t, " (Aida)"), " ")
	if rest == "" {
		return title
	}
	return rest
}

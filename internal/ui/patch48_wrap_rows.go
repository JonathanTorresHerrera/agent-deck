package ui

// Patch 48 (wrap rows). [display] wrap_rows draws each session in the classic
// list on two lines: the title alone on the first, so it is never cut to make
// room for badges, and the seat, age, tool, branch and other badges on the
// second, aligned under the title. Off by default; Settings → "Wrap rows".
//
// The classic list already measures rows through the embedded layout's
// variable-height helpers (scroll windowing, click hit-testing, page keys), so
// classicItemHeight is the one place that says how tall a classic row is.

import (
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/charmbracelet/lipgloss"
)

// wrappedBranchMax is the worktree branch length shown on the detail line,
// which has more room than the single-line row's 15.
const wrappedBranchMax = 40

// classicItemHeight is how many lines a classic-layout row draws. It must
// agree with renderItem: only real (not "creating") local sessions wrap.
func (h *Home) classicItemHeight(item session.Item) int {
	if h.wrapRows && item.Type == session.ItemTypeSession && item.CreatingID == "" && item.Session != nil {
		return 2
	}
	return 1
}

type wrappedSessionRow struct {
	item        session.Item
	selected    bool
	listWidth   int
	line1Prefix string // gutter, indent, selection, tree, chevron, status, space
	statusWidth int    // cells of the status glyph (+ provider badge)
	title       string
	titleStyle  lipgloss.Style
	details     []string // styled badges, each with its leading space; "" skipped
	subtitle    string   // plain pane title, dim, last
}

// wrappedContinuation is the tree column under a row's connector: │ while
// more siblings follow, blank under the last one.
func wrappedContinuation(item session.Item) string {
	last := item.IsLastInGroup
	if item.IsSubSession {
		last = item.IsLastSubSession
	}
	if last {
		return "  "
	}
	return TreeConnectorStyle.Render("│") + " "
}

// wrappedBaseIndent is the unselected indent (the selected sub-session row
// puts its ▶ in the indent; the detail line must not repeat it).
func wrappedBaseIndent(item session.Item) string {
	if item.Level <= 1 {
		return ""
	}
	if item.IsSubSession {
		groupIndent := strings.Repeat(treeEmpty, item.Level-2)
		if item.ParentIsLastInGroup {
			return groupIndent + "  "
		}
		return groupIndent + " " + TreeConnectorStyle.Render("│")
	}
	return strings.Repeat(treeEmpty, item.Level-1)
}

func (h *Home) renderWrappedSessionRow(b *strings.Builder, r wrappedSessionRow) {
	title := r.title
	if r.listWidth > 0 {
		budget := max(1, r.listWidth-cellWidth(r.line1Prefix)-1)
		if cellWidth(title) > budget {
			title = cellTruncate(title, budget, "…")
		}
	}
	b.WriteString(r.line1Prefix + r.titleStyle.Render(title))
	b.WriteString("\n")

	// Line 2 lines up under the title: same gutter and indent, the tree's
	// continuation under the connector, blanks under chevron and status.
	line := strings.Repeat(" ", leftGutterWidth) + wrappedBaseIndent(r.item) + " " +
		wrappedContinuation(r.item) + " " + strings.Repeat(" ", r.statusWidth)
	used := cellWidth(line)
	limit := r.listWidth - 1 // one cell of margin, as the single-line row keeps
	for _, d := range r.details {
		if d == "" {
			continue
		}
		w := cellWidth(d)
		if r.listWidth > 0 && used+w > limit {
			continue // drop a badge whole rather than cut it into a stub
		}
		line += d
		used += w
	}
	if r.subtitle != "" {
		room := limit - used - 1
		if r.listWidth <= 0 {
			room = cellWidth(r.subtitle)
		}
		if room > 10 {
			line += DimStyle.Render(" " + cellTruncate(r.subtitle, room, "…"))
		}
	}
	b.WriteString(line)
	b.WriteString("\n")
}

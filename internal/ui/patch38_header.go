package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/sysinfo"
)

// Patch 38 (responsive): the header (line 1) and the filter bar (line 2) used
// to be hard-clipped by MaxWidth on a small terminal, cutting the version badge
// and half a stats segment. They now degrade in a fixed order until they fit.
//
// Header line 1, in order: drop network, disk, ram, cpu (then gpu, load, which
// the spec leaves unordered) from the system-stats list, then the cost
// segment, then shorten the status words ("● 7 running" -> "● 7"). The status
// counts and the version badge are never dropped. Each step re-runs
// sysinfo.Format with a reduced show list; rendered ANSI strings are never
// chopped.

// headerSysDropOrder is the order system stats leave the header.
var headerSysDropOrder = []string{"network", "disk", "ram", "cpu", "gpu", "load"}

// headerLine1 is one rendered candidate for the left part of header line 1.
type headerLine1 struct {
	left  string
	sep   string
	parts []string
	kinds []session.Status
}

// buildHeaderStats renders the per-status stats (plus optional cost / system
// segments) as one string and returns the individual status parts for the
// patch-28 hitbox computation.
func (h *Home) buildHeaderStats(running, waiting, idle, stopped, errored int, short bool, cost, sys string) (stats string, sep string, parts []string, kinds []session.Status) {
	return h.buildHeaderStatsWith(running, waiting, idle, stopped, errored, short, true, cost, sys)
}

// buildHeaderStatsWith is buildHeaderStats with the [ui.header]
// sessions_by_status field honoured: statsOn=false renders no status counts
// (and no "no sessions" placeholder), only the cost and system segments.
func (h *Home) buildHeaderStatsWith(running, waiting, idle, stopped, errored int, short, statsOn bool, cost, sys string) (stats string, sep string, parts []string, kinds []session.Status) {
	sep = lipgloss.NewStyle().Foreground(ColorBorder).Render(" • ")

	// Patch 28 (status chips): each per-status segment is a click target, so
	// remember which status every parts entry stands for. A segment the user
	// toggled off renders dimmed and struck through.
	addStat := func(status session.Status, count int, glyph, word string, style lipgloss.Style) {
		if count <= 0 || !statsOn {
			return
		}
		if h.statusHidden(status) {
			style = lipgloss.NewStyle().Foreground(ColorComment).Faint(true).Strikethrough(true)
		}
		text := fmt.Sprintf("%s %d", glyph, count)
		if !short {
			text += " " + word
		}
		parts = append(parts, style.Render(text))
		kinds = append(kinds, status)
	}
	addStat(session.StatusRunning, running, "●", "running", lipgloss.NewStyle().Foreground(ColorGreen))
	addStat(session.StatusWaiting, waiting, "◐", "waiting", lipgloss.NewStyle().Foreground(ColorYellow))
	addStat(session.StatusIdle, idle, "○", "idle", lipgloss.NewStyle().Foreground(ColorText))
	// Issue #953: stopped sessions get their own segment so users can see
	// at a glance how many sessions are intentionally off vs. errored.
	addStat(session.StatusStopped, stopped, "■", "stopped", lipgloss.NewStyle().Foreground(ColorTextDim))
	addStat(session.StatusError, errored, "✕", "error", lipgloss.NewStyle().Foreground(ColorRed))

	var segs []string
	if len(parts) > 0 {
		segs = append(segs, strings.Join(parts, sep))
	} else if statsOn {
		segs = append(segs, lipgloss.NewStyle().Foreground(ColorText).Render("no sessions"))
	}
	if cost != "" {
		segs = append(segs, lipgloss.NewStyle().Foreground(ColorCyan).Render(cost))
	}
	if sys != "" {
		segs = append(segs, lipgloss.NewStyle().Foreground(ColorComment).Render(sys))
	}
	stats = strings.Join(segs, sep)
	return stats, sep, parts, kinds
}

// headerSysFormatted renders the system-stats segment for a reduced show list.
func (h *Home) headerSysFormatted(show []string) string {
	if h.sysStatsCollector == nil || len(show) == 0 {
		return ""
	}
	return sysinfo.Format(h.sysStatsCollector.Get(), h.sysStatsConfig.GetFormat(), show)
}

// reduceShow returns show without the first n entries of headerSysDropOrder.
func reduceShow(show []string, n int) []string {
	drop := make(map[string]bool, n)
	for i := 0; i < n && i < len(headerSysDropOrder); i++ {
		drop[headerSysDropOrder[i]] = true
	}
	out := make([]string, 0, len(show))
	for _, s := range show {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

// headerExtras carries what v1.16.22's header knows beyond patch 38's original
// inputs: the [ui.header].fields gates for the status counts and the system
// stats, and the opt-in accounts / ssh segments in full and compact form.
type headerExtras struct {
	statsOn, sysOn            bool
	accounts, accountsCompact string
	ssh, sshCompact           string
}

// fitHeaderLine1 renders the most detailed header-left candidate whose width
// plus the version badge plus the bar padding fits h.width. When nothing fits
// (a terminal narrower than logo+title+counts+version) the most degraded
// candidate is returned and the bar's MaxWidth clips it.
func (h *Home) fitHeaderLine1(logo, title, versionBadge string, running, waiting, idle, stopped, errored int, cost string) headerLine1 {
	return h.fitHeaderLine1With(logo, title, versionBadge, running, waiting, idle, stopped, errored, cost, headerExtras{statsOn: true, sysOn: true})
}

// fitHeaderLine1With is fitHeaderLine1 for the v1.16.22 header. Degrade order:
// the opt-in ssh then accounts segments go to their compact form, then away
// (upstream already treated them as the least protected); then patch 38's
// order: system stats one at a time (network, disk, ram, cpu, gpu, load), the
// cost, and finally the short status words. Counts and the version badge are
// never dropped. Unlike upstream's single "N sessions" fallback, every stage
// keeps one segment per status, so the patch-28 chip hitboxes stay clickable.
func (h *Home) fitHeaderLine1With(logo, title, versionBadge string, running, waiting, idle, stopped, errored int, cost string, x headerExtras) headerLine1 {
	show := h.sysStatsConfig.GetShow()
	badgeW := lipgloss.Width(versionBadge)

	var last headerLine1
	try := func(short, withCost bool, showN int, sysOn bool, extras []string) bool {
		c := ""
		if withCost {
			c = cost
		}
		sys := ""
		if sysOn && x.sysOn {
			sys = h.headerSysFormatted(reduceShow(show, showN))
		}
		stats, sep, parts, kinds := h.buildHeaderStatsWith(running, waiting, idle, stopped, errored, short, x.statsOn, c, sys)
		for _, e := range extras {
			if e != "" {
				if stats != "" {
					stats += sep
				}
				stats += e
			}
		}
		left := lipgloss.JoinHorizontal(lipgloss.Left, logo, "  ", title, "  ", stats)
		last = headerLine1{left: left, sep: sep, parts: parts, kinds: kinds}
		return h.width <= 0 || lipgloss.Width(left)+badgeW+2 <= h.width
	}

	for _, extras := range [][]string{
		{x.accounts, x.ssh},
		{x.accounts, x.sshCompact},
		{x.accountsCompact, x.sshCompact},
		{x.accountsCompact},
	} {
		if try(false, true, 0, true, extras) {
			return last
		}
	}
	for n := 0; n <= len(headerSysDropOrder); n++ {
		if try(false, true, n, true, nil) {
			return last
		}
	}
	if try(false, false, 0, false, nil) {
		return last
	}
	try(true, false, 0, false, nil)
	return last
}

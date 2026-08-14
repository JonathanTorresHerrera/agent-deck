package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// healthPanel is a full-screen diagnostic view (hotkey "health_panel",
// default Z) mirroring the costDashboard pattern: computed synchronously on
// open, refreshed on demand with r, scrolled with j/k.
type healthPanel struct {
	width, height int
	scrollOffset  int
	report        session.HealthReport
}

func newHealthPanel(instances []*session.Instance, width, height int) healthPanel {
	p := healthPanel{width: width, height: height}
	p.report = session.CollectHealthReport(instances)
	return p
}

func (p *healthPanel) refresh(instances []*session.Instance) {
	p.report = session.CollectHealthReport(instances)
	p.scrollOffset = 0
}

func healthGlyph(level string) string {
	switch level {
	case "ok":
		return lipgloss.NewStyle().Foreground(ColorGreen).Render("✓")
	case "warn":
		return lipgloss.NewStyle().Foreground(ColorYellow).Render("⚠")
	case "bad":
		return lipgloss.NewStyle().Foreground(ColorRed).Render("✕")
	default:
		return lipgloss.NewStyle().Foreground(ColorCyan).Render("•")
	}
}

func (p healthPanel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAccent)
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorText).Underline(true)
	nameStyle := lipgloss.NewStyle().Foreground(ColorText)
	detailStyle := lipgloss.NewStyle().Foreground(ColorComment)
	fixStyle := lipgloss.NewStyle().Foreground(ColorYellow).Italic(true)
	helpStyle := lipgloss.NewStyle().Foreground(ColorComment)

	warns, bads := 0, 0
	for _, s := range p.report.Sections {
		for _, it := range s.Items {
			switch it.Level {
			case "warn":
				warns++
			case "bad":
				bads++
			}
		}
	}
	verdict := lipgloss.NewStyle().Foreground(ColorGreen).Render("healthy")
	if bads > 0 {
		verdict = lipgloss.NewStyle().Foreground(ColorRed).Render(fmt.Sprintf("%d problem(s)", bads))
	} else if warns > 0 {
		verdict = lipgloss.NewStyle().Foreground(ColorYellow).Render(fmt.Sprintf("%d warning(s)", warns))
	}

	var lines []string
	lines = append(lines,
		titleStyle.Render(" Agent Deck Health")+"  —  "+verdict+
			detailStyle.Render("  (collected "+p.report.GeneratedAt.Format("15:04:05")+")"),
		"")

	for _, s := range p.report.Sections {
		lines = append(lines, "  "+sectionStyle.Render(s.Title))
		for _, it := range s.Items {
			lines = append(lines, fmt.Sprintf("  %s %s  %s",
				healthGlyph(it.Level), nameStyle.Render(it.Name), detailStyle.Render(it.Detail)))
			if it.Fix != "" {
				lines = append(lines, "      "+fixStyle.Render("→ "+it.Fix))
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, "  "+helpStyle.Render("r refresh • j/k scroll • q/esc close"))

	// Scroll window (header line always visible)
	maxLines := p.height - 1
	if maxLines < 5 {
		maxLines = 5
	}
	offset := p.scrollOffset
	if overflow := len(lines) - maxLines; overflow > 0 {
		if offset > overflow {
			offset = overflow
		}
	} else {
		offset = 0
	}
	end := offset + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[offset:end], "\n")
}

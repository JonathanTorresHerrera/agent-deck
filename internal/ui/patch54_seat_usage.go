package ui

// Patch 54 (seat usage in the preview). Under the PREVIEW tool header
// ("Claude @claude-c0e6cb2b") the pane shows the seat's subscription account,
// how much of each usage window is left with its reset time, and when that
// was observed. The data is ~/.agent-deck/seat-usage/<seat>.json, written by
// ~/.agent-deck/bin/seat-usage-refresh: hourly by seat-usage-refresh.timer,
// and by agent-deck itself after a seat switch (session.SwitchAttempt.Finish).
// No file, no lines: the section looks as it did before.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

type seatUsageWindow struct {
	Name       string  `json:"name"`
	Label      string  `json:"label"`
	UsedPct    float64 `json:"used_pct"`
	ResetsAt   string  `json:"resets_at"`
	ObservedAt string  `json:"observed_at"`
}

type seatUsage struct {
	Seat            string            `json:"seat"`
	Provider        string            `json:"provider"`
	Email           string            `json:"email"`
	Plan            string            `json:"plan"`
	Windows         []seatUsageWindow `json:"windows"`
	UsageObservedAt string            `json:"usage_observed_at"`
	CheckedAt       string            `json:"checked_at"`
	Note            string            `json:"note"`
	Credits         float64           `json:"credits"`
}

// seatUsageDir is overridable in tests.
var seatUsageDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agent-deck", "seat-usage")
}

var seatUsageCache struct {
	sync.Mutex
	entries map[string]seatUsageEntry
}

type seatUsageEntry struct {
	mtime time.Time
	usage *seatUsage
}

// loadSeatUsage returns the seat's usage file, re-reading it only when its
// mtime changed, so rendering the preview never parses JSON per frame.
func loadSeatUsage(seat string) *seatUsage {
	if seat == "" || strings.ContainsAny(seat, `/\`) || strings.HasPrefix(seat, ".") {
		return nil
	}
	dir := seatUsageDir()
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, seat+".json")
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	seatUsageCache.Lock()
	defer seatUsageCache.Unlock()
	if seatUsageCache.entries == nil {
		seatUsageCache.entries = map[string]seatUsageEntry{}
	}
	if e, ok := seatUsageCache.entries[path]; ok && e.mtime.Equal(st.ModTime()) {
		return e.usage
	}
	var u *seatUsage
	if data, err := os.ReadFile(path); err == nil {
		var v seatUsage
		if json.Unmarshal(data, &v) == nil {
			u = &v
		}
	}
	seatUsageCache.entries[path] = seatUsageEntry{mtime: st.ModTime(), usage: u}
	return u
}

// previewSeatName is the seat previewSeatSuffix names, without the " @".
func previewSeatName(inst *session.Instance) string {
	return strings.TrimPrefix(previewSeatSuffix(inst), " @")
}

var seatUsageNow = time.Now

// renderSeatUsageLines writes the Account / Usage / Checked lines for the
// selected session's seat, each fitted to width.
func renderSeatUsageLines(b *strings.Builder, inst *session.Instance, width int) {
	u := loadSeatUsage(previewSeatName(inst))
	if u == nil {
		return
	}
	labelStyle := lipgloss.NewStyle().Foreground(ColorText)
	valueStyle := lipgloss.NewStyle().Foreground(ColorText)
	dim := lipgloss.NewStyle().Foreground(ColorTextDim)
	now := seatUsageNow()
	line := func(label, value string, style lipgloss.Style) {
		room := max(1, width-cellWidth(label))
		b.WriteString(labelStyle.Render(label))
		b.WriteString(style.Render(cellTruncate(value, room, "…")))
		b.WriteString("\n")
	}

	if u.Email != "" || u.Plan != "" {
		acct := u.Email
		if u.Plan != "" {
			if acct != "" {
				acct += " · "
			}
			acct += seatPlanLabel(u.Plan)
		}
		line("Account: ", acct, valueStyle)
	}

	parts := make([]string, 0, len(u.Windows)+2)
	for _, w := range u.Windows {
		left := math.Max(0, 100-w.UsedPct)
		p := fmt.Sprintf("%s %s left", w.Label, trimPct(left))
		if t, ok := parseSeatTime(w.ResetsAt); ok {
			p += " (resets " + seatClock(t, now) + ")"
		}
		parts = append(parts, p)
	}
	if u.Credits > 0 {
		parts = append(parts, fmt.Sprintf("credits %.0f", u.Credits))
	}
	if u.Note != "" {
		parts = append(parts, u.Note)
	}
	usage := strings.Join(parts, " · ")
	if usage == "" {
		usage = "no usage reading yet"
	}
	style := valueStyle
	if seatUsageExhausted(u) {
		style = lipgloss.NewStyle().Foreground(ColorYellow)
	}
	line("Usage:   ", usage, style)

	checked := ""
	if t, ok := parseSeatTime(u.UsageObservedAt); ok {
		checked = "usage as of " + seatClock(t, now) + " (" + seatAgo(now.Sub(t)) + ")"
	}
	if t, ok := parseSeatTime(u.CheckedAt); ok {
		if checked != "" {
			checked += " · "
		}
		checked += "refreshed " + seatClock(t, now)
	}
	if checked != "" {
		line("Checked: ", checked, dim)
	}
}

func seatUsageExhausted(u *seatUsage) bool {
	if u.Note != "" {
		return true
	}
	for _, w := range u.Windows {
		if w.UsedPct >= 100 {
			return true
		}
	}
	return false
}

func trimPct(v float64) string {
	return fmt.Sprintf("%.0f%%", math.Round(v))
}

// seatPlanLabel turns codex plan ids ("self_serve_business_prolite") into
// words; Claude's planDisplayName is already words.
func seatPlanLabel(plan string) string {
	return strings.ReplaceAll(plan, "_", " ")
}

func parseSeatTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// seatClock is a local clock time, with the weekday when not today.
func seatClock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

func seatAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

package ui

// Patch 50 (status-pass profile). The footer's "status pass exceeds N ms
// budget" warning says a pass was slow, not where the time went. The per-minute
// health sample carries one total and one tmux-call count, which showed slow
// passes making ~3x the tmux calls of fast ones but not which stage made them.
//
// Each backgroundStatusUpdate now marks its stages (claims, tmux cache
// refresh, prep, the per-session status loop, snapshot, DB writes,
// notifications) with their wall time and tmux subprocess count. A pass at or
// over the fleet budget is appended to <health dir>/slow-passes.jsonl with its
// slowest sessions (rate-limited; see slowPassLogEvery). Diagnostic only:
// nothing reads the file back, and a write error is ignored.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/health"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// One over-budget pass every 5 s, and one under-budget pass a minute as the
// baseline to compare it with.
const (
	slowPassLogEvery = 5 * time.Second
	fastPassLogEvery = time.Minute
)

type passStage struct {
	Name  string  `json:"name"`
	MS    float64 `json:"ms"`
	Tmux  int64   `json:"tmux"`
	Polls int     `json:"polls,omitempty"`
}

type passProfile struct {
	start     time.Time
	lastAt    time.Time
	lastTmux  int64
	Stages    []passStage `json:"stages"`
	Polled    int         `json:"polled"`
	Forced    int         `json:"forced"`
	Slowest   []string    `json:"slowest,omitempty"`
	Sessions  int         `json:"sessions"`
	TotalMS   float64     `json:"total_ms"`
	BudgetMS  int64       `json:"budget_ms"`
	TmuxTotal int64       `json:"tmux_total"`
	TS        string      `json:"ts"`
	Over      bool        `json:"over"`
}

func newPassProfile(start time.Time, tmuxBefore int64) *passProfile {
	return &passProfile{start: start, lastAt: start, lastTmux: tmuxBefore}
}

// mark closes the stage that ran since the previous mark.
func (p *passProfile) mark(name string) {
	if p == nil {
		return
	}
	now, calls := time.Now(), tmux.SubprocessStarts()
	p.Stages = append(p.Stages, passStage{
		Name: name,
		MS:   float64(now.Sub(p.lastAt).Microseconds()) / 1000,
		Tmux: calls - p.lastTmux,
	})
	p.lastAt, p.lastTmux = now, calls
}

var slowPassLog struct {
	sync.Mutex
	lastSlow, lastFast time.Time
}

// finish writes the profile when the pass is over budget.
func (p *passProfile) finish(profile string, elapsed time.Duration, sessions int, calls int64) {
	if p == nil {
		return
	}
	budget := health.StatusPassBudgetFor(sessions)
	over := elapsed >= budget
	slowPassLog.Lock()
	last, every := &slowPassLog.lastFast, fastPassLogEvery
	if over {
		last, every = &slowPassLog.lastSlow, slowPassLogEvery
	}
	if time.Since(*last) < every {
		slowPassLog.Unlock()
		return
	}
	*last = time.Now()
	slowPassLog.Unlock()
	p.Over = over

	p.mark("rest")
	p.Sessions = sessions
	p.TotalMS = float64(elapsed.Microseconds()) / 1000
	p.BudgetMS = budget.Milliseconds()
	p.TmuxTotal = calls
	p.TS = time.Now().UTC().Format(time.RFC3339Nano)
	// Entries are "title=123ms"; slowest first.
	sorted := append([]string(nil), p.Slowest...)
	dur := func(s string) time.Duration {
		d, _ := time.ParseDuration(s[strings.LastIndex(s, "=")+1:])
		return d
	}
	sort.SliceStable(sorted, func(i, j int) bool { return dur(sorted[i]) > dur(sorted[j]) })
	if len(sorted) > 12 {
		sorted = sorted[:12]
	}
	p.Slowest = sorted
	dir, err := session.HealthLogDir(profile)
	if err != nil {
		return
	}
	line, err := json.Marshal(p)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "slow-passes.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

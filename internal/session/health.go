package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// Health report: a point-in-time diagnostic snapshot of the deck — phantom
// tmux sessions, ghost instances, duplicate conversation ids, missing
// transcripts, orphaned host processes, infra daemons, and storage growth.
// Collected on demand by the TUI's health panel (hotkey "health_panel").

type HealthItem struct {
	Level  string // "ok" | "info" | "warn" | "bad"
	Name   string
	Detail string
	Fix    string // suggested remedy; empty when none applies
}

type HealthSection struct {
	Title string
	Items []HealthItem
}

type HealthReport struct {
	GeneratedAt time.Time
	Sections    []HealthSection
}

func healthRun(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func healthAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func healthMB(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1<<20))
	default:
		return fmt.Sprintf("%.0fKB", float64(bytes)/(1<<10))
	}
}

func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// countTasklist returns how many Windows processes match the given image name,
// via WSL interop. Returns -1 when the count is unavailable.
func countTasklist(image string) int {
	out, err := healthRun(4*time.Second, "tasklist.exe", "/FO", "CSV", "/NH", "/FI", "IMAGENAME eq "+image)
	if err != nil {
		return -1
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, image) {
			n++
		}
	}
	return n
}

// CollectHealthReport gathers the full diagnostic snapshot. Runs several
// short-timeout subprocesses (tmux, loginctl, tasklist.exe); worst case a few
// seconds, which is acceptable for an explicitly requested panel.
func CollectHealthReport(instances []*Instance) HealthReport {
	r := HealthReport{GeneratedAt: time.Now()}

	live := func(st Status) bool {
		return st == StatusRunning || st == StatusWaiting || st == StatusIdle || st == StatusStarting
	}

	// ---- tmux <-> instance reconciliation -------------------------------
	var sess HealthSection
	sess.Title = "Sessions & tmux"

	sockets := map[string]bool{"": true}
	for _, inst := range instances {
		if inst.TmuxSocketName != "" {
			sockets[inst.TmuxSocketName] = true
		}
	}
	tmuxNames := map[string]bool{}
	tmuxUp := false
	for socket := range sockets {
		if names, err := tmux.ListSessionNamesOnSocket(socket); err == nil {
			tmuxUp = true
			for n := range names {
				tmuxNames[n] = true
			}
		}
	}

	instByTmux := map[string]*Instance{}
	liveCount := 0
	ghosts, phantoms, stale := 0, 0, 0
	for _, inst := range instances {
		if inst.IsArchived() {
			continue
		}
		name := ""
		if ts := inst.GetTmuxSession(); ts != nil {
			name = ts.Name
		}
		if name != "" {
			instByTmux[name] = inst
		}
		st := inst.GetStatusThreadSafe()
		if live(st) {
			liveCount++
			if name == "" || !tmuxNames[name] {
				ghosts++
				sess.Items = append(sess.Items, HealthItem{
					Level:  "bad",
					Name:   fmt.Sprintf("ghost: %q marked %s", inst.Title, st),
					Detail: "its tmux session no longer exists",
					Fix:    "restart it (R) or let the status sweep settle it to stopped",
				})
			}
		} else if name != "" && tmuxNames[name] {
			stale++
			sess.Items = append(sess.Items, HealthItem{
				Level:  "warn",
				Name:   fmt.Sprintf("alive but marked %s: %q", st, inst.Title),
				Detail: fmt.Sprintf("tmux session %s is still running", name),
				Fix:    "attach (Enter) to reconcile, or kill it if unwanted",
			})
		}
	}
	for name := range tmuxNames {
		if strings.HasPrefix(name, "agentdeck_") && instByTmux[name] == nil {
			phantoms++
			sess.Items = append(sess.Items, HealthItem{
				Level:  "warn",
				Name:   "phantom tmux session: " + name,
				Detail: "no deck instance references it",
				Fix:    "tmux kill-session -t " + name,
			})
		}
	}
	if !tmuxUp {
		sess.Items = append(sess.Items, HealthItem{
			Level: "info", Name: "tmux server not running", Detail: "no sessions are alive",
		})
	}
	if ghosts == 0 && phantoms == 0 && stale == 0 && tmuxUp {
		sess.Items = append(sess.Items, HealthItem{
			Level: "ok", Name: "tmux and session list agree",
			Detail: fmt.Sprintf("%d live session(s), no ghosts or phantoms", liveCount),
		})
	}
	r.Sections = append(r.Sections, sess)

	// ---- Claude conversations -------------------------------------------
	var conv HealthSection
	conv.Title = "Claude conversations"
	claims := map[string][]string{}
	missing := 0
	for _, inst := range instances {
		if inst.IsArchived() || !IsClaudeCompatible(inst.Tool) || inst.ClaudeSessionID == "" {
			continue
		}
		claims[inst.ClaudeSessionID] = append(claims[inst.ClaudeSessionID], inst.Title)
		if live(inst.GetStatusThreadSafe()) {
			if resolveClaudeTranscriptPath(GetClaudeConfigDirForInstance(inst), inst.ProjectPath, inst.ClaudeSessionID) == "" {
				missing++
				conv.Items = append(conv.Items, HealthItem{
					Level:  "warn",
					Name:   fmt.Sprintf("no transcript on disk: %q", inst.Title),
					Detail: "conversation " + inst.ClaudeSessionID[:8] + "… has no JSONL (deleted or never written)",
					Fix:    "a restart will begin with empty history",
				})
			}
		}
	}
	dupes := 0
	dupeIDs := make([]string, 0)
	for id, titles := range claims {
		if len(titles) > 1 {
			dupes++
			dupeIDs = append(dupeIDs, id)
		}
	}
	sort.Strings(dupeIDs)
	for _, id := range dupeIDs {
		conv.Items = append(conv.Items, HealthItem{
			Level:  "bad",
			Name:   "conversation claimed by multiple sessions",
			Detail: id[:8] + "… claimed by: " + strings.Join(claims[id], ", "),
			Fix:    "restart one of them so it forks its own conversation",
		})
	}
	if dupes == 0 && missing == 0 {
		conv.Items = append(conv.Items, HealthItem{
			Level: "ok", Name: "conversation bindings healthy",
			Detail: fmt.Sprintf("%d unique conversation id(s), all live transcripts present", len(claims)),
		})
	}
	r.Sections = append(r.Sections, conv)

	// ---- Host processes (WSL interop) -----------------------------------
	if isWSL() {
		var procs HealthSection
		procs.Title = "Host processes (Windows)"
		liveClaude := 0
		for _, inst := range instances {
			if !inst.IsArchived() && IsClaudeCompatible(inst.Tool) && live(inst.GetStatusThreadSafe()) {
				liveClaude++
			}
		}
		if n := countTasklist("claude.exe"); n >= 0 {
			item := HealthItem{
				Level:  "info",
				Name:   "claude.exe processes",
				Detail: fmt.Sprintf("%d running (deck has %d live claude session(s))", n, liveClaude),
			}
			if n > liveClaude+4 {
				item.Level = "warn"
				item.Fix = "possible orphaned claude processes (headless leftovers) — inspect with tasklist"
			}
			procs.Items = append(procs.Items, item)
		} else {
			procs.Items = append(procs.Items, HealthItem{
				Level: "info", Name: "claude.exe processes", Detail: "(unavailable — Windows interop off?)",
			})
		}
		if n := countTasklist("node.exe"); n >= 0 {
			item := HealthItem{
				Level:  "info",
				Name:   "node.exe processes",
				Detail: fmt.Sprintf("%d running", n),
			}
			if n > 20 {
				item.Level = "warn"
				item.Fix = "node.exe pile-up — known TUI-orphan pattern; kill node processes older than ~15 min"
			}
			procs.Items = append(procs.Items, item)
		}
		r.Sections = append(r.Sections, procs)
	}

	// ---- Infrastructure --------------------------------------------------
	var infra HealthSection
	infra.Title = "Infrastructure"

	home, _ := os.UserHomeDir()
	keepalive := filepath.Join(home, ".agent-deck", "bin", "keepalive.sh")
	if _, err := os.Stat(keepalive); err == nil {
		if out, err := healthRun(3*time.Second, "pgrep", "-f", "agent-deck/bin/keepalive"); err == nil && strings.TrimSpace(out) != "" {
			infra.Items = append(infra.Items, HealthItem{
				Level: "ok", Name: "keep-alive loop running",
				Detail: "WSL distro survives closing every terminal window",
			})
		} else {
			infra.Items = append(infra.Items, HealthItem{
				Level: "warn", Name: "keep-alive loop NOT running",
				Detail: "closing the last terminal may take the whole distro (and all sessions) down",
				Fix:    "start scheduled task AgentDeckKeepalive (or run keepalive.sh)",
			})
		}
	}
	if u, err := user.Current(); err == nil {
		if out, err := healthRun(3*time.Second, "loginctl", "show-user", u.Username, "--property=Linger"); err == nil {
			if strings.Contains(out, "Linger=yes") {
				infra.Items = append(infra.Items, HealthItem{
					Level: "ok", Name: "systemd lingering enabled",
					Detail: "tmux scopes survive logging out / quitting the TUI",
				})
			} else if strings.Contains(out, "Linger=no") {
				infra.Items = append(infra.Items, HealthItem{
					Level:  "warn",
					Name:   "systemd lingering DISABLED",
					Detail: "every agent-deck tmux scope dies when the last login session ends",
					Fix:    "loginctl enable-linger " + u.Username,
				})
			}
		}
	}
	if liveCount > 0 {
		newest := time.Time{}
		if entries, err := os.ReadDir(GetHooksDir()); err == nil {
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
					newest = fi.ModTime()
				}
			}
		}
		item := HealthItem{
			Level:  "ok",
			Name:   "hook events",
			Detail: "last event " + healthAgo(newest),
		}
		if newest.IsZero() || time.Since(newest) > 6*time.Hour {
			item.Level = "warn"
			item.Fix = "hook events not arriving — session-id tracking degrades; check the agent-deck hook wiring"
		}
		infra.Items = append(infra.Items, item)
	}
	if snap := filepath.Join(home, ".agent-deck", "running-snapshot"); true {
		if fi, err := os.Stat(snap); err == nil {
			infra.Items = append(infra.Items, HealthItem{
				Level: "info", Name: "running-snapshot",
				Detail: "updated " + healthAgo(fi.ModTime()),
			})
		}
	}
	if len(infra.Items) > 0 {
		r.Sections = append(r.Sections, infra)
	}

	// ---- Storage ---------------------------------------------------------
	var store HealthSection
	store.Title = "Storage"
	profile := os.Getenv("AGENTDECK_PROFILE")
	if profile == "" {
		profile = "default"
	}
	if dir, err := GetProfileDir(profile); err == nil {
		dbPath := filepath.Join(dir, "state.db")
		var dbSize, walSize int64
		if fi, err := os.Stat(dbPath); err == nil {
			dbSize = fi.Size()
		}
		if fi, err := os.Stat(dbPath + "-wal"); err == nil {
			walSize = fi.Size()
		}
		item := HealthItem{
			Level:  "info",
			Name:   "state.db",
			Detail: fmt.Sprintf("%s (+%s WAL)", healthMB(dbSize), healthMB(walSize)),
		}
		if walSize > 20*(1<<20) {
			item.Level = "warn"
			item.Fix = "large WAL — restart the TUI (or run a wal_checkpoint) to fold it back"
		}
		store.Items = append(store.Items, item)
	}
	total, archived := 0, 0
	for _, inst := range instances {
		total++
		if inst.IsArchived() {
			archived++
		}
	}
	store.Items = append(store.Items, HealthItem{
		Level: "info", Name: "instances",
		Detail: fmt.Sprintf("%d total (%d archived, %d live)", total, archived, liveCount),
	})
	var logBytes int64
	logDir := filepath.Join(home, ".agent-deck", "logs")
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil {
				logBytes += fi.Size()
			}
		}
		item := HealthItem{Level: "info", Name: "logs", Detail: healthMB(logBytes)}
		if logBytes > 200*(1<<20) {
			item.Level = "warn"
			item.Fix = "logs growing large — prune ~/.agent-deck/logs"
		}
		store.Items = append(store.Items, item)
	}
	r.Sections = append(r.Sections, store)

	// ---- System ----------------------------------------------------------
	var sys HealthSection
	sys.Title = "System"
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB int64
		for _, line := range strings.Split(string(data), "\n") {
			if _, err := fmt.Sscanf(line, "MemTotal: %d kB", &totalKB); err == nil {
				continue
			}
			_, _ = fmt.Sscanf(line, "MemAvailable: %d kB", &availKB)
		}
		if totalKB > 0 {
			usedPct := float64(totalKB-availKB) / float64(totalKB) * 100
			item := HealthItem{
				Level: "info", Name: "WSL memory",
				Detail: fmt.Sprintf("%s available of %s (%.0f%% used)", healthMB(availKB<<10), healthMB(totalKB<<10), usedPct),
			}
			if usedPct > 90 {
				item.Level = "warn"
				item.Fix = "memory pressure — heavy sessions may stall"
			}
			sys.Items = append(sys.Items, item)
		}
	}
	if boot := systemBootTime(); !boot.IsZero() {
		sys.Items = append(sys.Items, HealthItem{
			Level: "info", Name: "distro up since",
			Detail: boot.Local().Format("Jan 2 15:04") + " (" + healthAgo(boot) + ")",
		})
	}
	if len(sys.Items) > 0 {
		r.Sections = append(r.Sections, sys)
	}

	return r
}

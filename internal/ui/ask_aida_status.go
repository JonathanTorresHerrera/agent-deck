package ui

// Patch 24: notice when Aida has ANSWERED an ask made with `B`.
//
// A background tea.Tick (aidaStatusPollInterval) walks the asks that are
// still waiting on a reply — hub status accepted or held, a doorbell ref, no
// answered_at, asked within 48 h — and runs `ask-aida.sh --status` for them,
// one at a time, at most aidaStatusMaxPerTick per tick, oldest first. When the
// hub reports acked:true the record gains answered_at (in memory at once, in
// SQLite off the Update goroutine, as patch 21 does).
//
// Polling is background and silent: nothing here touches the status bar. A
// setup failure (the hub has not granted the read yet, or the script is not
// configured) pauses ALL polling for aidaStatusBackoff; any other failure
// skips that session until the next tick. With no script, nothing polls.
//
// Candidates come from the render snapshot, not from Instance getters: taking
// every Instance.mu on the Update goroutine each tick is the #1753 stall.
//
// Only one tick chain exists: the tick handler re-arms itself, the result
// handler never does. A tick that fires while a check is in flight (5 checks
// at 30 s can outlast one 90 s interval) skips without touching the queue.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// Package vars so tests can shrink them. Patch 24.
var (
	aidaStatusPollInterval = 90 * time.Second
	aidaStatusTimeout      = 30 * time.Second
	aidaStatusBackoff      = 30 * time.Minute
)

const aidaStatusMaxPerTick = 5

// aidaStatusRefPattern is the ref shape ask-aida.sh --status accepts; any
// other ref is refused there as bad_request, so it is never polled.
var aidaStatusRefPattern = regexp.MustCompile(`^doorbell-[A-Za-z0-9-]{1,72}$`)

// aidaAskEntry is one session's recorded ask.
type aidaAskEntry struct {
	id  string
	ask session.AidaAsk
}

type aidaStatusTickMsg struct{}

// aidaStatusResultMsg carries one --status run back to Update.
type aidaStatusResultMsg struct {
	deckID   string
	ref      string
	timedOut bool
	runErr   error
	result   aidaStatusResult
}

// aidaStatusResult is the script's one JSON line, loosely decoded like
// patch 19's askAidaResult.
type aidaStatusResult struct {
	OK    bool         `json:"ok"`
	Error askAidaText  `json:"error,omitempty"`
	Ref   askAidaText  `json:"ref,omitempty"`
	Acked askAidaTruth `json:"acked,omitempty"`
}

func (h *Home) aidaStatusTick() tea.Cmd {
	return tea.Tick(aidaStatusPollInterval, func(time.Time) tea.Msg {
		return aidaStatusTickMsg{}
	})
}

// aidaStatusCandidates picks the asks to check this tick: waiting on a reply,
// with a pollable ref, oldest first, at most aidaStatusMaxPerTick. Pure.
func aidaStatusCandidates(entries []aidaAskEntry, now time.Time) []aidaAskEntry {
	var out []aidaAskEntry
	for _, e := range entries {
		if e.ask.AwaitingReply(now) && aidaStatusRefPattern.MatchString(e.ask.Ref) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ask.At.Equal(out[j].ask.At) {
			return out[i].ask.At.Before(out[j].ask.At)
		}
		return out[i].id < out[j].id
	})
	if len(out) > aidaStatusMaxPerTick {
		out = out[:aidaStatusMaxPerTick]
	}
	return out
}

// handleAidaStatusTick starts this tick's checks and returns the first run,
// or nil when there is nothing to do. It does not re-arm the tick; Update
// does, so exactly one chain exists.
func (h *Home) handleAidaStatusTick() tea.Cmd {
	if h.aidaStatusInFlight {
		return nil
	}
	now := askAidaNow()
	if now.Before(h.aidaStatusPausedUntil) {
		return nil
	}
	if askAidaPreflight(askAidaScriptPath()) != "" {
		return nil
	}
	var entries []aidaAskEntry
	for id, st := range h.getSessionRenderSnapshot() {
		if st.aidaAskOK {
			entries = append(entries, aidaAskEntry{id: id, ask: st.aidaAsk})
		}
	}
	queue := aidaStatusCandidates(entries, now)
	if len(queue) == 0 {
		return nil
	}
	h.aidaStatusQueue = queue
	h.aidaStatusInFlight = true
	return h.nextAidaStatusCheck()
}

// nextAidaStatusCheck pops the queue and returns its run, or clears the
// in-flight flag when the queue is empty (or the script vanished).
func (h *Home) nextAidaStatusCheck() tea.Cmd {
	scriptPath := askAidaScriptPath()
	if len(h.aidaStatusQueue) == 0 || askAidaPreflight(scriptPath) != "" {
		h.aidaStatusQueue = nil
		h.aidaStatusInFlight = false
		return nil
	}
	e := h.aidaStatusQueue[0]
	h.aidaStatusQueue = h.aidaStatusQueue[1:]
	timeout := aidaStatusTimeout
	id, ref := e.id, e.ask.Ref
	return func() tea.Msg {
		return runAidaStatusScript(scriptPath, id, ref, timeout)
	}
}

// runAidaStatusScript runs `ask-aida.sh --status` once. It touches no Home
// state (it runs on a tea.Cmd goroutine).
func runAidaStatusScript(scriptPath, deckID, ref string, timeout time.Duration) aidaStatusResultMsg {
	out := aidaStatusResultMsg{deckID: deckID, ref: ref}
	body, err := json.Marshal(map[string]string{"ref": ref})
	if err != nil {
		out.runErr = err
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, scriptPath, "--status")
	cmd.Stdin = bytes.NewReader(append(body, '\n'))
	var stdout bytes.Buffer
	cmd.Stdout = &askAidaLimitedWriter{w: &stdout, n: askAidaMaxStdout}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second // same reason as runAskAidaScript
	runErr := cmd.Run()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		out.timedOut = true
		return out
	}
	line, ok := firstJSONLine(stdout.Bytes())
	if ok {
		if err := json.Unmarshal(line, &out.result); err == nil {
			return out
		}
	}
	if runErr != nil {
		out.runErr = runErr
	} else {
		out.runErr = errors.New("ask-aida.sh --status printed no JSON result")
	}
	return out
}

// firstJSONLine returns the first non-empty stdout line.
func firstJSONLine(raw []byte) ([]byte, bool) {
	for _, l := range bytes.Split(raw, []byte("\n")) {
		l = bytes.TrimSpace(l)
		if len(l) > 0 {
			return l, true
		}
	}
	return nil, false
}

// aidaStatusIsSetupError reports a failure no retry this tick can change:
// the hub has not granted the doorbell read yet, or the script is not set up.
func aidaStatusIsSetupError(code string) bool {
	return code == "mcp_error:capability_not_granted" || askAidaIsSetupError(code)
}

// applyAidaStatusResult applies one check. persist is the SQLite write for a
// newly answered ask (nil otherwise); next is the queue's next check (nil
// when this tick is done). Never touches the status bar.
func (h *Home) applyAidaStatusResult(msg aidaStatusResultMsg) (persist tea.Cmd, next tea.Cmd) {
	now := askAidaNow()
	res := msg.result
	switch {
	case msg.timedOut, msg.runErr != nil:
		// Skip this session until the next tick.
	case !res.OK:
		if aidaStatusIsSetupError(strings.TrimSpace(string(res.Error))) {
			h.aidaStatusPausedUntil = now.Add(aidaStatusBackoff)
			h.aidaStatusQueue = nil
			h.aidaStatusInFlight = false
			return nil, nil
		}
	case bool(res.Acked):
		persist = h.markAidaAskAnswered(msg.deckID, msg.ref, now)
	}
	return persist, h.nextAidaStatusCheck()
}

// markAidaAskAnswered sets answered_at in memory when the session's record is
// still the ask that was checked, patches the render snapshot so the row
// marker clears at once, and returns the SQLite write.
func (h *Home) markAidaAskAnswered(deckID, ref string, now time.Time) tea.Cmd {
	h.instancesMu.RLock()
	inst := h.instanceByID[deckID]
	h.instancesMu.RUnlock()
	if inst == nil {
		return nil
	}
	record, ok := inst.MarkLastAidaAskAnswered(ref, now)
	if !ok {
		return nil
	}
	if rec, ok := session.ParseAidaAsk(record); ok {
		h.patchAidaAskInSnapshot(deckID, rec, true)
	}
	return persistLastAidaAskCmd(deckID, record)
}

// persistLastAidaAskCmd writes the record off the Update goroutine.
func persistLastAidaAskCmd(id string, record json.RawMessage) tea.Cmd {
	return func() tea.Msg {
		db := statedb.GetGlobal()
		if db == nil {
			return nil
		}
		if err := db.WriteLastAidaAsk(id, record); err != nil {
			uiLog.Debug("last_aida_ask_persist_failed",
				slog.String("instance", id),
				slog.String("error", err.Error()),
			)
		}
		return nil
	}
}

// patchAidaAskInSnapshot updates one session's ask in the render snapshot
// (copy-on-write) so the row marker follows a new ask or an answer without
// waiting for the next refresh. A refresh already in flight may publish the
// old value; the next one corrects it.
func (h *Home) patchAidaAskInSnapshot(id string, rec session.AidaAsk, ok bool) {
	h.sessionRenderSnapshotMu.Lock()
	defer h.sessionRenderSnapshotMu.Unlock()
	prev := h.getSessionRenderSnapshot()
	state, had := prev[id]
	if !had {
		return
	}
	snap := make(map[string]sessionRenderState, len(prev))
	for k, v := range prev {
		snap[k] = v
	}
	state.aidaAsk, state.aidaAskOK = rec, ok
	snap[id] = state
	h.sessionRenderSnapshot.Store(snap)
}

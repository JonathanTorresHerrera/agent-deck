package ui

// Patch 19: Ask Aida (WO-DECK-ASK-AIDA-01).
//
// `B` on a session row opens a one-line dialog; Enter hands the session's
// identity plus an optional note to ~/.agent-deck/bin/ask-aida.sh, which calls
// the Ops hub's ops_ask_aida_about_session MCP tool. The hub posts to the
// session's project bus channel and rings Aida's doorbell; the deck only
// reports what the hub says happened. The binary holds no credentials and does
// no network I/O of its own.
//
// Idempotency lives in the hub, keyed by a UUID v4 requestKey. The deck keeps
// one pending ask per session (askAidaPending) until a terminal result, so a
// retry after an unconfirmed result finishes the SAME ask instead of ringing
// again. The pending entry freezes the whole request, not just the key: the
// hub fingerprints the payload (status and note included), and a rebuilt
// request — whose status has drifted since the first press — would be refused
// as idempotency_key_reused, leaving the session stuck. So a `B` on a session
// with a pending ask resends the frozen request directly, without the dialog.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// askAidaScriptPath resolves the sender script. A package var so tests point
// it at a stub in t.TempDir() and never reach ~/.agent-deck.
var askAidaScriptPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".agent-deck", "bin", "ask-aida.sh")
}

// askAidaTimeout bounds one script run. The script caps its own curls under
// this. A package var so the timeout test does not wait 45 s.
var askAidaTimeout = 45 * time.Second

// askAidaNow is the clock the cooldown text is computed against (tests pin it).
var askAidaNow = time.Now

const askAidaMaxStdout = 64 << 10

// The hub refuses the whole ask when an optional field fails its pattern, so
// the deck drops such a field rather than lose the ask.
var (
	askAidaNativeIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	askAidaEnumPattern     = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

// askAidaRequest is the one JSON object written to the script's stdin. Field
// names match the hub tool's input.
type askAidaRequest struct {
	RequestKey      string `json:"requestKey"`
	DeckID          string `json:"deckId"`
	Title           string `json:"title"`
	Group           string `json:"group"`
	Path            string `json:"path"`
	NativeSessionID string `json:"nativeSessionId,omitempty"`
	Tool            string `json:"tool,omitempty"`
	Status          string `json:"status,omitempty"`
	Note            string `json:"note,omitempty"`
}

// askAidaResult is the one JSON line the script prints: the hub tool's output
// plus ok:true, or {ok:false, error, httpStatus?}. Only the fields the deck
// reads are declared, and they decode loosely: one mistyped field (a 0/1
// fallback, a numeric ref) must not turn a real ring into UNCONFIRMED — the
// replayed receipt would carry the same types on every retry.
type askAidaResult struct {
	OK         bool         `json:"ok"`
	Error      askAidaText  `json:"error,omitempty"`
	HTTPStatus askAidaText  `json:"httpStatus,omitempty"`
	Reason     askAidaText  `json:"reason,omitempty"`
	Status     askAidaText  `json:"status,omitempty"`
	RequestKey askAidaText  `json:"requestKey,omitempty"`
	Ref        askAidaText  `json:"ref,omitempty"`
	Refusal    askAidaText  `json:"refusal,omitempty"`
	BusProject askAidaText  `json:"busProject,omitempty"`
	BusChannel askAidaText  `json:"busChannel,omitempty"`
	Fallback   askAidaTruth `json:"fallback,omitempty"`
	CreatedAt  askAidaText  `json:"createdAt,omitempty"`
}

// askAidaText decodes a JSON string or number as text; anything else is "".
type askAidaText string

func (t *askAidaText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = askAidaText(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*t = askAidaText(n.String())
		return nil
	}
	*t = ""
	return nil
}

// askAidaTruth decodes true/false, 1/0 and "true"/"1" as a bool.
type askAidaTruth bool

func (v *askAidaTruth) UnmarshalJSON(b []byte) error {
	switch strings.Trim(strings.TrimSpace(string(b)), `"`) {
	case "true", "1":
		*v = true
	default:
		*v = false
	}
	return nil
}

// askAidaSubmitMsg is emitted by the dialog on Enter. An empty note means the
// hub's default ask.
type askAidaSubmitMsg struct {
	instanceID string
	note       string
}

// askAidaResultMsg carries one script run back to Update. Exactly one of
// notSetUp, runErr or result applies: notSetUp is a missing/unusable script,
// runErr a timeout or a script that printed no parseable JSON line.
type askAidaResultMsg struct {
	deckID     string
	requestKey string
	title      string
	notSetUp   string
	timedOut   bool
	runErr     error
	result     askAidaResult
}

// askAidaPendingEntry is one session's unfinished ask: the key and the exact
// request it was first sent with.
type askAidaPendingEntry struct {
	req askAidaRequest
}

// Outcome of one result for the pending map.
type askAidaKeyAction int

const (
	askAidaKeepKey askAidaKeyAction = iota
	askAidaClearKey
)

const (
	askAidaUnconfirmedText = "UNCONFIRMED — press B to retry the same ask (no new ring)"
	askAidaFallbackSuffix  = " (no project channel — sent to #aida-ops)"
	askAidaGroupRowText    = "Select a session to ask Aida about it"
)

// newAskAidaRequestKey returns a random UUID v4 from crypto/rand.
func newAskAidaRequestKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// buildAskAidaRequest snapshots inst into the request the hub fingerprints.
func buildAskAidaRequest(inst *session.Instance, requestKey, note string) askAidaRequest {
	path := inst.ProjectPath
	if inst.IsWorktree() && inst.WorktreePath != "" {
		path = inst.WorktreePath
	}
	native := inst.DisplaySessionID()
	if native == "" {
		native = inst.ClaudeSessionID
	}
	if !askAidaNativeIDPattern.MatchString(native) {
		native = ""
	}
	tool := strings.ToLower(strings.TrimSpace(inst.Tool))
	if !askAidaEnumPattern.MatchString(tool) {
		tool = ""
	}
	status := strings.ToLower(string(inst.GetStatusThreadSafe()))
	if !askAidaEnumPattern.MatchString(status) {
		status = ""
	}
	return askAidaRequest{
		RequestKey:      requestKey,
		DeckID:          inst.ID,
		Title:           inst.Title,
		Group:           inst.GroupPath,
		Path:            path,
		NativeSessionID: native,
		Tool:            tool,
		Status:          status,
		Note:            strings.TrimSpace(note),
	}
}

// askAidaPreflight reports why the script cannot run, or "" when it can.
func askAidaPreflight(path string) string {
	if path == "" {
		return "cannot resolve the home directory"
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "ask-aida.sh not found at " + path
		}
		return "ask-aida.sh unreadable: " + err.Error()
	}
	if info.IsDir() {
		return path + " is a directory"
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "ask-aida.sh is not executable"
	}
	return ""
}

// runAskAidaScript runs the script once and returns its result message. It
// touches no Home state (it runs on a tea.Cmd goroutine).
func runAskAidaScript(scriptPath string, req askAidaRequest, title string, timeout time.Duration) askAidaResultMsg {
	out := askAidaResultMsg{deckID: req.DeckID, requestKey: req.RequestKey, title: title}
	body, err := json.Marshal(req)
	if err != nil {
		out.runErr = fmt.Errorf("encode request: %w", err)
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Stdin = bytes.NewReader(append(body, '\n'))
	var stdout bytes.Buffer
	cmd.Stdout = &askAidaLimitedWriter{w: &stdout, n: askAidaMaxStdout}
	cmd.Stderr = io.Discard
	// Killing the script leaves a curl grandchild holding the stdout pipe;
	// WaitDelay force-closes the pipes so Wait returns. The orphan dies on its
	// own --max-time.
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		out.timedOut = true
		return out
	}
	// The script prints its JSON line on failure too, so parse stdout
	// whatever the exit code; only no parseable line is a script error.
	if res, ok := parseAskAidaOutput(stdout.Bytes()); ok {
		out.result = res
		return out
	}
	if runErr != nil {
		out.runErr = fmt.Errorf("ask-aida.sh: %w", runErr)
	} else {
		out.runErr = errors.New("ask-aida.sh printed no JSON result")
	}
	return out
}

// parseAskAidaOutput takes the first non-empty stdout line that is a JSON
// object.
func parseAskAidaOutput(raw []byte) (askAidaResult, bool) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 4096), askAidaMaxStdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var res askAidaResult
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			return askAidaResult{}, false
		}
		return res, true
	}
	return askAidaResult{}, false
}

type askAidaLimitedWriter struct {
	w io.Writer
	n int
}

func (l *askAidaLimitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	chunk := p
	if len(chunk) > l.n {
		chunk = chunk[:l.n]
	}
	_, _ = l.w.Write(chunk)
	l.n -= len(chunk)
	return len(p), nil
}

// askAidaRefPart renders " — ref <ref>", or "" when the hub returned none.
func askAidaRefPart(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return ""
	}
	return " — ref " + ref
}

// askAidaChannelLabel renders "project/#channel" from the hub's result.
func askAidaChannelLabel(project, channel string) string {
	channel = strings.TrimPrefix(strings.TrimSpace(channel), "#")
	project = strings.TrimSpace(project)
	switch {
	case project == "" && channel == "":
		return "Aida"
	case project == "":
		return "#" + channel
	case channel == "":
		return project
	}
	return project + "/#" + channel
}

// askAidaCooldownText renders "Already asked N min ago — ref …".
func askAidaCooldownText(createdAt, ref string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(createdAt))
	if err != nil {
		// SQLite's datetime('now') shape, in UTC.
		t, err = time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(createdAt), time.UTC)
	}
	if err != nil {
		return "Already asked recently" + askAidaRefPart(ref)
	}
	mins := int(now.Sub(t).Minutes())
	if mins < 0 {
		mins = 0
	}
	return fmt.Sprintf("Already asked %d min ago", mins) + askAidaRefPart(ref)
}

// askAidaIsSetupError reports script codes that mean "not set up yet".
func askAidaIsSetupError(code string) bool {
	return code == "not_configured" || code == "bad_env_file"
}

// askAidaMCPErrorIsFinal reports an MCP-level failure that a same-key retry
// cannot change: an access decision, a key the hub has bound to another
// payload, a JSON-RPC rejection (e.g. the tool does not exist yet), or an
// input the hub refused. The hub's catch-all operation_unavailable is the one
// case where the tool may have posted before failing, so it stays retryable.
func askAidaMCPErrorIsFinal(code string) bool {
	c := strings.TrimPrefix(code, "mcp_error:")
	return c != "operation_unavailable"
}

// askAidaOutcome maps one script run to the status-bar text and what happens
// to the session's pending key. It is pure: Update applies the key action.
func askAidaOutcome(msg askAidaResultMsg, now time.Time) (string, askAidaKeyAction) {
	switch {
	case msg.notSetUp != "":
		return "Ask Aida not set up: " + msg.notSetUp, askAidaKeepKey
	case msg.timedOut, msg.runErr != nil:
		return askAidaUnconfirmedText, askAidaKeepKey
	}
	res := msg.result
	if !res.OK {
		code := strings.TrimSpace(string(res.Error))
		switch {
		case askAidaIsSetupError(code):
			text := "Ask Aida not set up: " + code
			if string(res.Reason) != "" {
				text += " (" + string(res.Reason) + ")"
			}
			return text, askAidaKeepKey
		case strings.HasPrefix(code, "mcp_error:") && askAidaMCPErrorIsFinal(code):
			return "NOT notified: " + code, askAidaClearKey
		case code == "":
			return askAidaUnconfirmedText, askAidaKeepKey
		}
		return askAidaUnconfirmedText + " (" + code + ")", askAidaKeepKey
	}

	suffix := ""
	if res.Fallback {
		suffix = askAidaFallbackSuffix
	}
	switch string(res.Status) {
	case "accepted":
		return "Aida notified → " + askAidaChannelLabel(string(res.BusProject), string(res.BusChannel)) +
			askAidaRefPart(string(res.Ref)) + suffix, askAidaClearKey
	case "held":
		return "Held until 7 AM PT" + askAidaRefPart(string(res.Ref)) + suffix, askAidaClearKey
	case "routed":
		return "Logged, not delivered" + askAidaRefPart(string(res.Ref)) + suffix, askAidaClearKey
	case "refused":
		refusal := string(res.Refusal)
		if refusal == "" {
			refusal = "refused"
		}
		return "NOT notified: " + refusal + suffix, askAidaClearKey
	case "cooldown":
		return askAidaCooldownText(string(res.CreatedAt), string(res.Ref), now) + suffix, askAidaClearKey
	case "in_progress":
		// The hub is still working another ask for this session. Nothing was
		// created for this press, so drop the key: the next press is a fresh
		// ask, which the hub answers with that ask's outcome (cooldown), or
		// finishes it if it was orphaned. Adopting the hub's key would resend
		// this press's payload under it and be refused as
		// idempotency_key_reused.
		return "Aida is still being asked about this session — press B in a moment to see the result" + suffix, askAidaClearKey
	case "indeterminate":
		return askAidaUnconfirmedText + suffix, askAidaKeepKey
	case "failed":
		if string(res.Error) == "" {
			return "NOT notified: failed" + suffix, askAidaKeepKey
		}
		return "NOT notified: " + string(res.Error) + suffix, askAidaClearKey
	}
	return askAidaUnconfirmedText + fmt.Sprintf(" (unexpected status %q)", string(res.Status)) + suffix, askAidaKeepKey
}

// askAidaTarget resolves the row under the cursor to the session to ask
// about: a session row itself, or a window sub-row's parent session.
func (h *Home) askAidaTarget() *session.Instance {
	if h.cursor < 0 || h.cursor >= len(h.flatItems) {
		return nil
	}
	item := h.flatItems[h.cursor]
	switch item.Type {
	case session.ItemTypeSession:
		return item.Session
	case session.ItemTypeWindow:
		return h.getInstanceByID(item.WindowSessionID)
	}
	return nil
}

// handleAskAidaKey is the `B` hotkey.
func (h *Home) handleAskAidaKey() tea.Cmd {
	inst := h.askAidaTarget()
	if inst == nil {
		h.setError(fmt.Errorf("%s", askAidaGroupRowText))
		return nil
	}
	if h.askAidaInFlight[inst.ID] {
		h.setError(fmt.Errorf("%s", "Still sending the earlier ask — wait for its result"))
		return nil
	}
	if pending, ok := h.askAidaPending[inst.ID]; ok {
		// A retry finishes the earlier ask with its frozen request.
		return h.sendAskAida(pending.req, inst.Title, true)
	}
	if reason := askAidaPreflight(askAidaScriptPath()); reason != "" {
		h.setError(fmt.Errorf("Ask Aida not set up: %s", reason))
		return nil
	}
	h.promptInputDialog.ShowAskAida(inst.ID, inst.Title)
	// Patch 21: warn when this session was already asked.
	h.promptInputDialog.SetAskAidaWarning(askAidaAlreadyAskedLine(inst, askAidaNow()))
	return nil
}

// handleAskAidaSubmit turns the dialog's Enter into a new ask.
func (h *Home) handleAskAidaSubmit(msg askAidaSubmitMsg) tea.Cmd {
	h.instancesMu.RLock()
	inst := h.instanceByID[msg.instanceID]
	h.instancesMu.RUnlock()
	if inst == nil {
		h.setError(fmt.Errorf("%s", "Ask Aida: that session no longer exists"))
		return nil
	}
	if h.askAidaInFlight[inst.ID] {
		h.setError(fmt.Errorf("%s", "Still sending the earlier ask — wait for its result"))
		return nil
	}
	if pending, ok := h.askAidaPending[inst.ID]; ok {
		return h.sendAskAida(pending.req, inst.Title, true)
	}
	key, err := newAskAidaRequestKey()
	if err != nil {
		h.setError(fmt.Errorf("Ask Aida: cannot generate a request key: %v", err))
		return nil
	}
	return h.sendAskAida(buildAskAidaRequest(inst, key, msg.note), inst.Title, false)
}

// sendAskAida records the pending ask and returns the Cmd that runs the
// script. All map writes happen here or in applyAskAidaResult, on the Update
// goroutine; the Cmd captures values only.
func (h *Home) sendAskAida(req askAidaRequest, title string, retry bool) tea.Cmd {
	scriptPath := askAidaScriptPath()
	if reason := askAidaPreflight(scriptPath); reason != "" {
		h.setError(fmt.Errorf("Ask Aida not set up: %s", reason))
		return nil
	}
	if h.askAidaPending == nil {
		h.askAidaPending = make(map[string]*askAidaPendingEntry)
	}
	if h.askAidaInFlight == nil {
		h.askAidaInFlight = make(map[string]bool)
	}
	h.askAidaPending[req.DeckID] = &askAidaPendingEntry{req: req}
	h.askAidaInFlight[req.DeckID] = true
	if retry {
		h.setError(fmt.Errorf("Retrying the earlier ask about %q…", title))
	} else {
		h.setError(fmt.Errorf("Asking Aida about %q…", title))
	}
	timeout := askAidaTimeout
	return func() tea.Msg {
		return runAskAidaScript(scriptPath, req, title, timeout)
	}
}

// applyAskAidaResult updates the pending map and the status bar.
//
// Patch 21: an ask that reached the doorbell also records last_aida_ask. The
// in-memory record is set here, on the Update goroutine, so the preview shows
// it on the next render; the returned Cmd does the SQLite write, which can
// stall for seconds under SQLITE_BUSY and so must not run on this goroutine.
func (h *Home) applyAskAidaResult(msg askAidaResultMsg) tea.Cmd {
	delete(h.askAidaInFlight, msg.deckID)
	now := askAidaNow()
	text, action := askAidaOutcome(msg, now)
	if action == askAidaClearKey {
		delete(h.askAidaPending, msg.deckID)
	}
	h.setError(fmt.Errorf("%s", text))

	rec, ok := askAidaRecordFor(msg, now)
	if !ok {
		return nil
	}
	h.instancesMu.RLock()
	inst := h.instanceByID[msg.deckID]
	h.instancesMu.RUnlock()
	if inst == nil {
		return nil
	}
	record := inst.SetLastAidaAsk(rec)
	id := inst.ID
	// Patch 24: the row's 🔔 appears now, not at the next snapshot refresh.
	if parsed, ok := session.ParseAidaAsk(record); ok {
		h.patchAidaAskInSnapshot(id, parsed, true)
	}
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

// Patch 21: the hub statuses that mean the ask reached Aida's doorbell, and
// the word each one shows in the preview and the dialog.
var aidaAskWords = map[string]string{
	"accepted": "notified",
	"held":     "held until 7 AM",
	"routed":   "logged, not delivered",
}

// aidaAskWord renders a recorded status. Patch 21.
func aidaAskWord(status string) string {
	if w, ok := aidaAskWords[status]; ok {
		return w
	}
	return status
}

// askAidaRecordFor returns the last_aida_ask record for one script run, and
// false for every outcome that did not reach the doorbell: a setup error, a
// timeout, a script error, ok:false, and the refused / cooldown / in_progress
// / indeterminate / failed statuses. Pure. Patch 21.
//
// Deliberately not derived from askAidaOutcome's key action: refused and
// cooldown clear the key too, and neither rang anything.
func askAidaRecordFor(msg askAidaResultMsg, now time.Time) (session.AidaAsk, bool) {
	if msg.notSetUp != "" || msg.timedOut || msg.runErr != nil || !msg.result.OK {
		return session.AidaAsk{}, false
	}
	res := msg.result
	status := string(res.Status)
	if _, ok := aidaAskWords[status]; !ok {
		return session.AidaAsk{}, false
	}
	channel := ""
	if strings.TrimSpace(string(res.BusProject)) != "" || strings.TrimSpace(strings.TrimPrefix(string(res.BusChannel), "#")) != "" {
		channel = askAidaChannelLabel(string(res.BusProject), string(res.BusChannel))
	}
	return session.NewAidaAsk(now, status, string(res.Ref), channel, bool(res.Fallback)), true
}

// askAidaAlreadyAskedLine is the `B` dialog's warning for a session that has
// a record, or "" without one. Patch 21.
func askAidaAlreadyAskedLine(inst *session.Instance, now time.Time) string {
	if inst == nil {
		return ""
	}
	rec, ok := inst.LastAidaAsk()
	if !ok {
		return ""
	}
	// Patch 24: say whether Aida has answered.
	if answeredAt, answered := rec.Answered(); answered {
		return "Aida answered " + humanizeSince(now.Sub(answeredAt)) + " — Enter asks again"
	}
	waiting := ""
	if rec.AwaitingReply(now) {
		waiting = ", no reply yet"
	}
	return "Already asked Aida " + humanizeSince(now.Sub(rec.At)) +
		" (" + aidaAskWord(rec.Status) + ")" + waiting + " — Enter asks again"
}

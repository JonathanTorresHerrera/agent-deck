// Patch 24: notice when Aida has ANSWERED an ask made with `B`.
//
// A background poller runs `ask-aida.sh --status` for asks still waiting on a
// reply and records answered_at once the hub reports acked:true. The preview,
// the `B` dialog and the session row then say so; the row carries a 🔔 only
// while the ask is unanswered.
//
// Scripts are a stub in t.TempDir(); the DB is a temp statedb. Nothing reaches
// ~/.agent-deck or the network.
package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// --- status stub -----------------------------------------------------------

type aidaStatusStub struct {
	dir string
}

// newAidaStatusStub points askAidaScriptPath at a stub that records its
// argument and stdin and prints resp-<ref> (or response) for the ref it was
// asked about.
func newAidaStatusStub(t *testing.T) *aidaStatusStub {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s' "$1" > %[1]q/args
cat > %[1]q/req.json
n=$(cat %[1]q/count 2>/dev/null || echo 0)
echo $((n+1)) > %[1]q/count
ref=$(sed -n 's/.*"ref":"\([^"]*\)".*/\1/p' %[1]q/req.json)
echo "$ref" >> %[1]q/refs
if [ -f %[1]q/sleep ]; then exec sleep 5; fi
if [ -f %[1]q/resp-"$ref" ]; then cat %[1]q/resp-"$ref"; else cat %[1]q/response; fi
`, dir)
	path := filepath.Join(dir, "ask-aida.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prevPath, prevTimeout := askAidaScriptPath, aidaStatusTimeout
	askAidaScriptPath = func() string { return path }
	aidaStatusTimeout = 10 * time.Second
	t.Cleanup(func() { askAidaScriptPath, aidaStatusTimeout = prevPath, prevTimeout })
	s := &aidaStatusStub{dir: dir}
	s.respond(t, "", `{"ok":true,"ref":"x","status":"accepted","acked":false,"ackCheck":"checked"}`)
	return s
}

// respond sets the reply for one ref, or the default reply when ref is "".
func (s *aidaStatusStub) respond(t *testing.T, ref, line string) {
	t.Helper()
	name := "response"
	if ref != "" {
		name = "resp-" + ref
	}
	if err := os.WriteFile(filepath.Join(s.dir, name), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *aidaStatusStub) runs() int {
	raw, err := os.ReadFile(filepath.Join(s.dir, "count"))
	if err != nil {
		return 0
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &n)
	return n
}

func (s *aidaStatusStub) refs() []string {
	raw, _ := os.ReadFile(filepath.Join(s.dir, "refs"))
	return strings.Fields(string(raw))
}

// --- helpers ---------------------------------------------------------------

// statusHome is armAskAidaHome plus extra sessions, each with a waiting ask.
func statusHome(t *testing.T, now time.Time, refs ...string) (*Home, []*session.Instance) {
	t.Helper()
	home, first := armAskAidaHome(t)
	insts := []*session.Instance{first}
	for i := 1; i < len(refs); i++ {
		insts = append(insts, session.NewInstanceWithTool(fmt.Sprintf("s%d", i), "/tmp", "claude"))
	}
	home.instancesMu.Lock()
	home.instances = insts
	home.instanceByID = map[string]*session.Instance{}
	for _, in := range insts {
		home.instanceByID[in.ID] = in
	}
	home.instancesMu.Unlock()
	for i, in := range insts {
		// Older refs first: refs[0] is the oldest ask.
		at := now.Add(-time.Duration(len(refs)-i) * time.Hour)
		in.SetLastAidaAsk(session.NewAidaAsk(at, "accepted", refs[i], "vita-ehr/#build", false))
	}
	home.refreshSessionRenderSnapshot(nil)
	return home, insts
}

// pollOnce runs one queued status check and applies its result. It returns
// the next check's Cmd (nil when the tick's queue is done).
func pollOnce(t *testing.T, h *Home, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a status check, got no Cmd")
	}
	msg, ok := cmd().(aidaStatusResultMsg)
	if !ok {
		t.Fatalf("status Cmd returned %T", msg)
	}
	persist, next := h.applyAidaStatusResult(msg)
	if persist != nil {
		_ = persist()
	}
	return next
}

func tickCmd(h *Home) tea.Cmd {
	return h.handleAidaStatusTick()
}

func sentinelErr(h *Home) {
	h.setError(errors.New("sentinel"))
}

func errText(h *Home) string {
	if h.err == nil {
		return ""
	}
	return h.err.Error()
}

// --- candidate selection ---------------------------------------------------

func TestPatch24_CandidateSelection(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	mk := func(id, status, ref string, age time.Duration) aidaAskEntry {
		return aidaAskEntry{id: id, ask: session.NewAidaAsk(now.Add(-age), status, ref, "", false)}
	}
	entries := []aidaAskEntry{
		mk("newer", "accepted", "doorbell-a", time.Hour),
		mk("routed", "routed", "doorbell-b", 2*time.Hour),
		mk("noref", "accepted", "", 2*time.Hour),
		mk("old", "held", "doorbell-c", 47*time.Hour),
		mk("expired", "accepted", "doorbell-d", 49*time.Hour),
		{id: "answered", ask: session.NewAidaAsk(now.Add(-3*time.Hour), "accepted", "doorbell-e", "", false).WithAnswered(now)},
		mk("badref", "accepted", "ring-1", 5*time.Hour),
		mk("mid", "held", "doorbell-f", 3*time.Hour),
	}
	var got []string
	for _, e := range aidaStatusCandidates(entries, now) {
		got = append(got, e.id)
	}
	want := []string{"old", "mid", "newer"} // oldest first
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("candidates = %v, want %v", got, want)
	}

	// At most five per tick.
	var many []aidaAskEntry
	for i := 0; i < 8; i++ {
		many = append(many, mk(fmt.Sprintf("s%d", i), "accepted", fmt.Sprintf("doorbell-%d", i), time.Duration(i+1)*time.Minute))
	}
	if n := len(aidaStatusCandidates(many, now)); n != 5 {
		t.Errorf("%d candidates, want at most 5", n)
	}
}

// --- acked result ----------------------------------------------------------

func TestPatch24_AckedSetsAnsweredAndPersists(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, insts := statusHome(t, now, "doorbell-1")
	inst := insts[0]
	db := withAskAidaDB(t, inst)

	// A record with a field this binary does not know.
	withExtras, ok := session.ParseAidaAsk(json.RawMessage(
		`{"at":` + fmt.Sprint(now.Add(-time.Hour).Unix()) + `,"status":"accepted","ref":"doorbell-1","channel":"vita-ehr/#build","fallback":false,"future":{"x":1}}`))
	if !ok {
		t.Fatal("fixture did not parse")
	}
	inst.SetLastAidaAsk(withExtras)
	home.refreshSessionRenderSnapshot(nil)
	stub.respond(t, "doorbell-1", `{"ok":true,"ref":"doorbell-1","status":"accepted","acked":true,"ackCheck":"checked"}`)
	sentinelErr(home)

	cmd := tickCmd(home)
	if cmd == nil {
		t.Fatal("tick ran no status check")
	}
	if next := pollOnce(t, home, cmd); next != nil {
		t.Error("one candidate should leave nothing queued")
	}

	args, _ := os.ReadFile(filepath.Join(stub.dir, "args"))
	if string(args) != "--status" {
		t.Errorf("script argument = %q, want --status", args)
	}
	req, _ := os.ReadFile(filepath.Join(stub.dir, "req.json"))
	var reqObj map[string]any
	if err := json.Unmarshal(req, &reqObj); err != nil || len(reqObj) != 1 || reqObj["ref"] != "doorbell-1" {
		t.Errorf("status request = %s, want exactly {\"ref\":\"doorbell-1\"}", req)
	}

	rec, _ := inst.LastAidaAsk()
	if at, answered := rec.Answered(); !answered || !at.Equal(now) {
		t.Fatalf("in-memory record not answered: %+v", rec)
	}
	var obj map[string]any
	if err := json.Unmarshal(dbToolData(t, db, inst.ID)["last_aida_ask"], &obj); err != nil {
		t.Fatal(err)
	}
	if obj["answered_at"] != float64(now.Unix()) || obj["ref"] != "doorbell-1" || obj["status"] != "accepted" {
		t.Errorf("DB record = %v", obj)
	}
	if f, ok := obj["future"].(map[string]any); !ok || f["x"] != float64(1) {
		t.Errorf("unknown field lost in the DB: %v", obj)
	}
	if errText(home) != "sentinel" {
		t.Errorf("polling touched the status bar: %q", errText(home))
	}

	// Answered: the next tick has nothing to check.
	if tickCmd(home) != nil {
		t.Error("an answered ask was polled again")
	}
}

func TestPatch24_NotAckedLeavesRecord(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, insts := statusHome(t, now, "doorbell-1")
	_ = pollOnce(t, home, tickCmd(home))
	if rec, _ := insts[0].LastAidaAsk(); rec.IsAnswered() {
		t.Error("acked:false marked the ask answered")
	}
	if stub.runs() != 1 {
		t.Errorf("runs = %d", stub.runs())
	}
}

// A new `B` ask that lands while an old ring is being checked stays
// unanswered: the ack belongs to the old ring.
func TestPatch24_NewAskMidPollStaysUnanswered(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, insts := statusHome(t, now, "doorbell-old")
	stub.respond(t, "doorbell-old", `{"ok":true,"ref":"doorbell-old","status":"accepted","acked":true}`)

	cmd := tickCmd(home)
	msg := cmd()
	insts[0].SetLastAidaAsk(session.NewAidaAsk(now, "accepted", "doorbell-new", "", false))
	persist, _ := home.applyAidaStatusResult(msg.(aidaStatusResultMsg))
	if persist != nil {
		t.Error("an ack for the old ring scheduled a write")
	}
	rec, _ := insts[0].LastAidaAsk()
	if rec.Ref != "doorbell-new" || rec.IsAnswered() {
		t.Errorf("new ask = %+v, want unanswered doorbell-new", rec)
	}
}

// --- back-off --------------------------------------------------------------

func TestPatch24_SetupErrorsBackOffAllPolling(t *testing.T) {
	for _, code := range []string{"mcp_error:capability_not_granted", "not_configured"} {
		t.Run(code, func(t *testing.T) {
			stub := newAidaStatusStub(t)
			now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			clock := now
			prev := askAidaNow
			askAidaNow = func() time.Time { return clock }
			t.Cleanup(func() { askAidaNow = prev })

			home, _ := statusHome(t, now, "doorbell-1", "doorbell-2", "doorbell-3")
			stub.respond(t, "", `{"ok":false,"error":"`+code+`"}`)
			sentinelErr(home)

			if next := pollOnce(t, home, tickCmd(home)); next != nil {
				t.Error("a setup error must stop this tick's queue")
			}
			if stub.runs() != 1 {
				t.Errorf("runs = %d, want 1", stub.runs())
			}
			if home.aidaStatusInFlight {
				t.Error("in-flight flag left set after back-off")
			}
			clock = now.Add(29 * time.Minute)
			if tickCmd(home) != nil {
				t.Error("polled during the 30 min back-off")
			}
			clock = now.Add(31 * time.Minute)
			if tickCmd(home) == nil {
				t.Error("polling did not resume after 30 min")
			}
			if errText(home) != "sentinel" {
				t.Errorf("poll error surfaced in the status bar: %q", errText(home))
			}
		})
	}
}

func TestPatch24_OtherErrorSkipsOnlyThatSession(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, insts := statusHome(t, now, "doorbell-1", "doorbell-2")
	stub.respond(t, "doorbell-1", `{"ok":false,"error":"http_502","httpStatus":502}`)
	stub.respond(t, "doorbell-2", `{"ok":true,"ref":"doorbell-2","status":"held","acked":true}`)
	sentinelErr(home)

	next := pollOnce(t, home, tickCmd(home))
	if next == nil {
		t.Fatal("an ordinary error stopped the queue")
	}
	if pollOnce(t, home, next) != nil {
		t.Error("queue should be done")
	}
	if rec, _ := insts[1].LastAidaAsk(); !rec.IsAnswered() {
		t.Error("the second session was not checked")
	}
	if got := strings.Join(stub.refs(), ","); got != "doorbell-1,doorbell-2" {
		t.Errorf("checked %s", got)
	}
	// The failed session is tried again on the next tick.
	if tickCmd(home) == nil {
		t.Error("the failed session was not retried on the next tick")
	}
	if errText(home) != "sentinel" {
		t.Errorf("poll error surfaced: %q", errText(home))
	}
}

func TestPatch24_TimeoutSkipsSession(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, insts := statusHome(t, now, "doorbell-1")
	if err := os.WriteFile(filepath.Join(stub.dir, "sleep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	aidaStatusTimeout = 300 * time.Millisecond
	start := time.Now()
	_ = pollOnce(t, home, tickCmd(home))
	if time.Since(start) > 4*time.Second {
		t.Errorf("timeout did not bound the run: %v", time.Since(start))
	}
	if rec, _ := insts[0].LastAidaAsk(); rec.IsAnswered() {
		t.Error("timeout marked answered")
	}
	if !home.aidaStatusPausedUntil.IsZero() {
		t.Error("a timeout must not back off all polling")
	}
}

// --- one at a time ---------------------------------------------------------

func TestPatch24_AtMostOneInFlight(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	refs := []string{}
	for i := 0; i < 7; i++ {
		refs = append(refs, fmt.Sprintf("doorbell-%d", i))
	}
	home, _ := statusHome(t, now, refs...)

	cmd := tickCmd(home)
	if cmd == nil {
		t.Fatal("no status check")
	}
	if !home.aidaStatusInFlight {
		t.Error("in-flight flag not set")
	}
	// A tick while a check is in flight starts nothing and keeps the queue.
	queued := len(home.aidaStatusQueue)
	if tickCmd(home) != nil {
		t.Error("second tick started a check while one was in flight")
	}
	if len(home.aidaStatusQueue) != queued {
		t.Error("second tick replaced the queue")
	}
	if stub.runs() != 0 {
		t.Errorf("a tick ran the script itself (%d runs)", stub.runs())
	}

	// The queue drains one check at a time, five per tick, oldest first.
	n := 0
	for c := cmd; c != nil; n++ {
		before := stub.runs()
		c = pollOnce(t, home, c)
		if stub.runs() != before+1 {
			t.Fatalf("a check ran %d scripts", stub.runs()-before)
		}
	}
	if n != 5 {
		t.Errorf("tick ran %d checks, want 5", n)
	}
	if got := strings.Join(stub.refs(), ","); got != "doorbell-0,doorbell-1,doorbell-2,doorbell-3,doorbell-4" {
		t.Errorf("order = %s", got)
	}
	if home.aidaStatusInFlight {
		t.Error("in-flight flag left set after the queue drained")
	}
}

func TestPatch24_NoScriptNoPolling(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	prev := askAidaScriptPath
	askAidaScriptPath = func() string { return filepath.Join(t.TempDir(), "missing.sh") }
	t.Cleanup(func() { askAidaScriptPath = prev })
	home, _ := statusHome(t, now, "doorbell-1")
	sentinelErr(home)
	if tickCmd(home) != nil {
		t.Error("polled with no script")
	}
	if home.aidaStatusInFlight || errText(home) != "sentinel" {
		t.Error("a missing script changed state or the status bar")
	}
}

func TestPatch24_TickMsgRearms(t *testing.T) {
	home, _ := armAskAidaHome(t)
	if _, cmd := home.updateInner(aidaStatusTickMsg{}); cmd == nil {
		t.Error("the tick message did not re-arm the poller")
	}
}

// An open dialog must not swallow the poller's messages: a dropped tick kills
// the only chain, a dropped result leaves the in-flight flag set for good.
func TestPatch24_MessagesSurviveOpenDialog(t *testing.T) {
	newAidaStatusStub(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, _ := statusHome(t, now, "doorbell-1")
	pressAskAida(home)
	if !home.promptInputDialog.IsAskAida() {
		t.Fatal("dialog did not open")
	}
	if _, cmd := home.Update(aidaStatusTickMsg{}); cmd == nil {
		t.Fatal("tick swallowed while the dialog is open")
	}
	if !home.aidaStatusInFlight {
		t.Fatal("tick under an open dialog started no check")
	}
	msg := runAidaStatusScript(askAidaScriptPath(), home.instances[0].ID, "doorbell-1", aidaStatusTimeout)
	home.Update(msg)
	if home.aidaStatusInFlight {
		t.Error("result swallowed while the dialog is open: in-flight flag stuck")
	}
}

// --- preview ---------------------------------------------------------------

func TestPatch24_PreviewLine(t *testing.T) {
	at := time.Now().Add(-25 * time.Minute).Truncate(time.Second)
	stamp := formatActivityStamp(at, false)
	answeredAt := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
	ans := formatActivityStamp(answeredAt, false)
	rel := formatRelativeTime(at)
	cases := []struct {
		name string
		a    session.AidaAsk
		want string
	}{
		{"accepted waiting", session.NewAidaAsk(at, "accepted", "doorbell-1", "", false),
			"🔔 asked Aida: " + stamp + " — notified · waiting for reply"},
		{"held waiting fallback", session.NewAidaAsk(at, "held", "doorbell-1", "", true),
			"🔔 asked Aida: " + stamp + " — held until 7 AM · waiting for reply · #aida-ops fallback"},
		{"routed never waits", session.NewAidaAsk(at, "routed", "doorbell-1", "", false),
			"🔔 asked Aida: " + stamp + " — logged, not delivered"},
		{"old ask stops waiting", session.NewAidaAsk(time.Now().Add(-50*time.Hour), "accepted", "doorbell-1", "", false),
			"🔔 asked Aida: " + formatActivityStamp(time.Now().Add(-50*time.Hour), false) + " — notified"},
		{"answered", session.NewAidaAsk(at, "accepted", "doorbell-1", "", false).WithAnswered(answeredAt),
			"✅ Aida answered: " + ans + " (asked " + rel + ")"},
		{"answered fallback", session.NewAidaAsk(at, "held", "doorbell-1", "", true).WithAnswered(answeredAt),
			"✅ Aida answered: " + ans + " (asked " + rel + ") · #aida-ops fallback"},
	}
	for _, tc := range cases {
		inst := session.NewInstanceWithTool("p24", "/tmp", "claude")
		inst.SetLastAidaAsk(tc.a)
		if got := lastAidaAskPreviewLine(inst); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.name, got, tc.want)
		}
	}
}

// --- dialog ----------------------------------------------------------------

func TestPatch24_DialogWarning(t *testing.T) {
	newAskAidaStub(t)
	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, inst := armAskAidaHome(t)

	inst.SetLastAidaAsk(session.NewAidaAsk(now.Add(-2*time.Hour), "accepted", "doorbell-1", "", false))
	waiting := "Already asked Aida " + humanizeSince(2*time.Hour) + " (notified), no reply yet — Enter asks again"
	if got := askAidaAlreadyAskedLine(inst, now); got != waiting {
		t.Errorf("unanswered:\n got  %q\n want %q", got, waiting)
	}

	inst.SetLastAidaAsk(session.NewAidaAsk(now.Add(-2*time.Hour), "accepted", "doorbell-1", "", false).WithAnswered(now.Add(-20 * time.Minute)))
	answered := "Aida answered " + humanizeSince(20*time.Minute) + " — Enter asks again"
	if got := askAidaAlreadyAskedLine(inst, now); got != answered {
		t.Errorf("answered:\n got  %q\n want %q", got, answered)
	}
	pressAskAida(home)
	if view := home.promptInputDialog.View(""); !strings.Contains(view, answered) {
		t.Errorf("dialog missing the answered line:\n%s", view)
	}

	// Routed never waits for a reply: patch 21's wording, unchanged.
	inst.SetLastAidaAsk(session.NewAidaAsk(now.Add(-2*time.Hour), "routed", "doorbell-1", "", false))
	routed := "Already asked Aida " + humanizeSince(2*time.Hour) + " (logged, not delivered) — Enter asks again"
	if got := askAidaAlreadyAskedLine(inst, now); got != routed {
		t.Errorf("routed:\n got  %q\n want %q", got, routed)
	}
}

// --- list row --------------------------------------------------------------

func renderAidaRow(t *testing.T, inst *session.Instance, width int) string {
	t.Helper()
	forceTrueColorProfile()
	h := &Home{width: width}
	h.refreshSessionRenderSnapshot([]*session.Instance{inst})
	item := session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1, Path: "test", IsLastInGroup: true}
	var b strings.Builder
	h.renderSessionItem(&b, item, false, h.getSessionRenderSnapshot(), width)
	return strings.TrimSuffix(b.String(), "\n")
}

func TestPatch24_RowMarkerOnlyWhileUnanswered(t *testing.T) {
	now := time.Now()
	mkInst := func() *session.Instance {
		return &session.Instance{ID: "p24-row", Title: "row", Tool: "claude", CreatedAt: now}
	}
	bare := renderAidaRow(t, mkInst(), 140)
	if strings.Contains(bare, "🔔") {
		t.Fatalf("no record, yet a marker: %q", bare)
	}

	waiting := mkInst()
	waiting.SetLastAidaAsk(session.NewAidaAsk(now.Add(-time.Hour), "accepted", "doorbell-1", "", false))
	row := renderAidaRow(t, waiting, 140)
	if !strings.Contains(row, "🔔") {
		t.Errorf("unanswered ask shows no marker: %q", row)
	}
	if cellWidth(row) != cellWidth(bare)+cellWidth(" 🔔") {
		t.Errorf("marker width: %d vs bare %d", cellWidth(row), cellWidth(bare))
	}

	for name, a := range map[string]session.AidaAsk{
		"answered": session.NewAidaAsk(now.Add(-time.Hour), "accepted", "doorbell-1", "", false).WithAnswered(now),
		"expired":  session.NewAidaAsk(now.Add(-49*time.Hour), "accepted", "doorbell-1", "", false),
		"routed":   session.NewAidaAsk(now.Add(-time.Hour), "routed", "doorbell-1", "", false),
		"no ref":   session.NewAidaAsk(now.Add(-time.Hour), "held", "", "", false),
	} {
		inst := mkInst()
		inst.SetLastAidaAsk(a)
		if got := renderAidaRow(t, inst, 140); got != bare {
			t.Errorf("%s: row differs from a row with no record:\n got  %q\n want %q", name, got, bare)
		}
	}

	// A long title at a narrow width still fits with the marker.
	long := mkInst()
	long.Title = strings.Repeat("very long session title ", 10)
	long.SetLastAidaAsk(session.NewAidaAsk(now.Add(-time.Hour), "held", "doorbell-1", "", false))
	narrow := renderAidaRow(t, long, 50)
	if cellWidth(narrow) > 50 {
		t.Errorf("row is %d cells wide at listWidth 50: %q", cellWidth(narrow), narrow)
	}
	if !strings.Contains(narrow, "🔔") {
		t.Errorf("marker truncated away: %q", narrow)
	}
}

// The marker clears as soon as the poller marks the ask answered, without
// waiting for the next snapshot refresh.
func TestPatch24_RowMarkerClearsOnAnswer(t *testing.T) {
	stub := newAidaStatusStub(t)
	now := time.Now()
	pinAskAidaNow(t, now)
	home, insts := statusHome(t, now, "doorbell-1")
	stub.respond(t, "doorbell-1", `{"ok":true,"ref":"doorbell-1","status":"accepted","acked":true}`)
	state := home.getSessionRenderSnapshot()[insts[0].ID]
	if !state.aidaAskOK || !state.aidaAsk.AwaitingReply(now) {
		t.Fatal("snapshot does not carry the waiting ask")
	}
	_ = pollOnce(t, home, tickCmd(home))
	state = home.getSessionRenderSnapshot()[insts[0].ID]
	if !state.aidaAsk.IsAnswered() {
		t.Error("snapshot still shows the ask unanswered")
	}
}

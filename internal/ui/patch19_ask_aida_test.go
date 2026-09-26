// Patch 19: Ask Aida (WO-DECK-ASK-AIDA-01).
//
// `B` on a session row asks Aida, through ~/.agent-deck/bin/ask-aida.sh and
// the Ops hub, to check that session. These tests pin the binding and help
// entry, the group-row guard, the JSON handed to the script, the pending-key
// rules (reuse until a terminal result) and the status text for every row of
// the spec's table.
//
// Every script here is a stub written into t.TempDir(); askAidaScriptPath is
// pointed at it. Nothing reaches ~/.agent-deck, the real ask-aida.sh, or the
// network.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// askAidaStub is a fake ask-aida.sh. It saves stdin to req.json and prints
// the contents of response (or sleeps / fails when told to).
type askAidaStub struct {
	dir  string
	path string
}

func newAskAidaStub(t *testing.T) *askAidaStub {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
cat > %[1]q/req.json
n=$(cat %[1]q/count 2>/dev/null || echo 0)
echo $((n+1)) > %[1]q/count
if [ -f %[1]q/sleep ]; then exec sleep 5; fi
if [ -f %[1]q/fail ]; then echo "boom" >&2; exit 1; fi
cat %[1]q/response
`, dir)
	path := filepath.Join(dir, "ask-aida.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prevPath, prevTimeout := askAidaScriptPath, askAidaTimeout
	askAidaScriptPath = func() string { return path }
	askAidaTimeout = 10 * time.Second
	t.Cleanup(func() { askAidaScriptPath, askAidaTimeout = prevPath, prevTimeout })
	return &askAidaStub{dir: dir, path: path}
}

func (s *askAidaStub) respond(t *testing.T, line string) {
	t.Helper()
	_ = os.Remove(filepath.Join(s.dir, "sleep"))
	_ = os.Remove(filepath.Join(s.dir, "fail"))
	if err := os.WriteFile(filepath.Join(s.dir, "response"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *askAidaStub) flag(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.dir, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *askAidaStub) lastRequest(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.dir, "req.json"))
	if err != nil {
		t.Fatalf("stub never received a request: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("request is not one JSON object: %v\n%s", err, raw)
	}
	return m
}

func (s *askAidaStub) runs(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.dir, "count"))
	if err != nil {
		return 0
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &n)
	return n
}

// armAskAidaHome builds a Home whose cursor sits on one claude session row,
// under a group row.
func armAskAidaHome(t *testing.T) (*Home, *session.Instance) {
	t.Helper()
	home := NewHome()
	home.width = 120
	home.height = 40
	home.initialLoading = false

	inst := session.NewInstanceWithTool("WO-TEST ask aida", "/repos/vita-ehr", "claude")
	inst.GroupPath = "vita-ehr"
	inst.ClaudeSessionID = "0f5e2a4c-1b2d-4e3f-8a9b-0c1d2e3f4a5b"

	home.instancesMu.Lock()
	home.instances = []*session.Instance{inst}
	home.instanceByID = map[string]*session.Instance{inst.ID: inst}
	home.instancesMu.Unlock()

	home.flatItems = []session.Item{
		{Type: session.ItemTypeGroup, Path: "vita-ehr", Level: 0},
		{Type: session.ItemTypeSession, Session: inst, Level: 1},
	}
	home.cursor = 1
	return home, inst
}

func pressAskAida(h *Home) tea.Cmd {
	_, cmd := h.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(defaultHotkeyBindings[hotkeyAskAida])})
	return cmd
}

// submitAskAida drives the dialog's Enter through updateInner and returns the
// script Cmd.
func submitAskAida(t *testing.T, h *Home, note string) tea.Cmd {
	t.Helper()
	if !h.promptInputDialog.IsAskAida() {
		t.Fatal("Ask Aida dialog is not open")
	}
	h.promptInputDialog.input.SetValue(note)
	d, cmd := h.promptInputDialog.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.promptInputDialog = d
	if cmd == nil {
		t.Fatal("Enter in the Ask Aida dialog emitted nothing")
	}
	_, sendCmd := h.updateInner(cmd())
	return sendCmd
}

// finish runs a script Cmd and feeds its result back through updateInner.
func finish(t *testing.T, h *Home, cmd tea.Cmd) askAidaResultMsg {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a script run, got no Cmd (status: %v)", h.err)
	}
	msg, ok := cmd().(askAidaResultMsg)
	if !ok {
		t.Fatalf("script Cmd returned %T, want askAidaResultMsg", msg)
	}
	h.updateInner(msg)
	return msg
}

func statusText(h *Home) string {
	if h.err == nil {
		return ""
	}
	return h.err.Error()
}

// --- binding, help, conflicts ---------------------------------------------

func TestPatch19_BindingDefaultsToB(t *testing.T) {
	if got := defaultHotkeyBindings[hotkeyAskAida]; got != "B" {
		t.Fatalf("ask_aida default = %q, want %q", got, "B")
	}
	found := false
	for _, action := range hotkeyActionOrder {
		if action == hotkeyAskAida {
			found = true
		}
	}
	if !found {
		t.Fatal("hotkeyAskAida missing from hotkeyActionOrder")
	}
	// Rebindable like any other action.
	b := resolveHotkeys(map[string]string{"ask_aida": "alt+b"})
	if b[hotkeyAskAida] != "alt+b" {
		t.Errorf("override not applied, got %q", b[hotkeyAskAida])
	}
	lookup, _ := buildHotkeyLookup(b)
	if lookup["alt+b"] != "B" {
		t.Errorf("rebound key should dispatch to the canonical B case, got %q", lookup["alt+b"])
	}
}

func TestPatch19_BHasNoConflict(t *testing.T) {
	for action, key := range defaultHotkeyBindings {
		if action == hotkeyAskAida {
			continue
		}
		for _, alias := range append(hotkeyAliases(key), defaultTriggersForAction(action)...) {
			if alias == "B" || alias == "shift+b" {
				t.Errorf("action %q also claims %q", action, alias)
			}
		}
	}
	clauses := map[string]bool{}
	for _, binding := range mainDispatcherBindings(t) {
		for _, alias := range hotkeyAliases(binding.key) {
			if alias == "B" || alias == "shift+b" {
				clauses[binding.action] = true
			}
		}
	}
	if len(clauses) != 1 {
		t.Errorf("B/shift+b is handled by %d dispatcher clauses, want exactly 1: %v", len(clauses), clauses)
	}
}

func TestPatch19_HelpOverlayListsAskAida(t *testing.T) {
	overlay := NewHelpOverlay()
	overlay.SetHotkeys(resolveHotkeys(nil))
	overlay.SetSize(140, 400)
	overlay.Show()
	view := overlay.View()
	if !strings.Contains(view, "Ask Aida to check this session") {
		t.Fatalf("help overlay missing the Ask Aida entry:\n%s", view)
	}
}

// --- row guards and dialog ------------------------------------------------

func TestPatch19_GroupRowShowsGuardAndNoDialog(t *testing.T) {
	stub := newAskAidaStub(t)
	home, _ := armAskAidaHome(t)
	home.cursor = 0 // the group row

	if cmd := pressAskAida(home); cmd != nil {
		t.Error("B on a group row must not start anything")
	}
	if home.promptInputDialog.IsVisible() {
		t.Error("B on a group row must not open the dialog")
	}
	if got := statusText(home); got != "Select a session to ask Aida about it" {
		t.Errorf("status = %q", got)
	}
	if stub.runs(t) != 0 {
		t.Error("the script ran for a group row")
	}
}

func TestPatch19_SessionRowOpensDialog(t *testing.T) {
	newAskAidaStub(t)
	home, inst := armAskAidaHome(t)

	pressAskAida(home)
	if !home.promptInputDialog.IsAskAida() {
		t.Fatal("B on a session row should open the Ask Aida dialog")
	}
	if home.promptInputDialog.instanceID != inst.ID {
		t.Errorf("dialog bound to %q, want %q", home.promptInputDialog.instanceID, inst.ID)
	}
	view := home.promptInputDialog.View("")
	for _, want := range []string{`Ask Aida about "WO-TEST ask aida"`, "Enter send · Esc cancel · empty = default ask"} {
		if !strings.Contains(view, want) {
			t.Errorf("dialog missing %q:\n%s", want, view)
		}
	}
	// Esc cancels without a send.
	d, cmd := home.promptInputDialog.Update(tea.KeyMsg{Type: tea.KeyEsc})
	home.promptInputDialog = d
	if cmd != nil || d.IsVisible() {
		t.Error("Esc should close the dialog without sending")
	}
	// The prompt-session dialog does not inherit the mode.
	home.promptInputDialog.Show(inst.ID, inst.Title)
	if home.promptInputDialog.IsAskAida() {
		t.Error("Show must reset the Ask Aida mode")
	}
}

func TestPatch19_EmptyEnterSendsDefaultAsk(t *testing.T) {
	d := NewPromptInputDialog()
	d.ShowAskAida("id-1", "t")
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("empty Enter must still submit (default ask)")
	}
	msg, ok := cmd().(askAidaSubmitMsg)
	if !ok || msg.instanceID != "id-1" || msg.note != "" {
		t.Fatalf("got %#v", cmd())
	}
}

func TestPatch19_MissingOrNonExecutableScriptIsNotSetUp(t *testing.T) {
	home, _ := armAskAidaHome(t)
	dir := t.TempDir()
	prev := askAidaScriptPath
	t.Cleanup(func() { askAidaScriptPath = prev })

	askAidaScriptPath = func() string { return filepath.Join(dir, "missing.sh") }
	pressAskAida(home)
	if home.promptInputDialog.IsVisible() || !strings.HasPrefix(statusText(home), "Ask Aida not set up: ") {
		t.Errorf("missing script: status %q", statusText(home))
	}

	plain := filepath.Join(dir, "plain.sh")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	askAidaScriptPath = func() string { return plain }
	pressAskAida(home)
	if got := statusText(home); got != "Ask Aida not set up: ask-aida.sh is not executable" {
		t.Errorf("non-executable script: status %q", got)
	}
}

// --- request shape --------------------------------------------------------

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestPatch19_RequestJSONShape(t *testing.T) {
	stub := newAskAidaStub(t)
	stub.respond(t, `{"ok":true,"status":"accepted","ref":"r1","busProject":"vita-ehr","busChannel":"build"}`)
	home, inst := armAskAidaHome(t)
	inst.WorktreePath = "/repos/vita-ehr-worktrees/feat"
	inst.WorktreeRepoRoot = "/repos/vita-ehr"
	inst.WorktreeBranch = "feat"

	pressAskAida(home)
	finish(t, home, submitAskAida(t, home, "  is it stuck?  "))
	req := stub.lastRequest(t)

	wantKeys := []string{"requestKey", "deckId", "title", "group", "path", "nativeSessionId", "tool", "status", "note"}
	for _, k := range wantKeys {
		if _, ok := req[k]; !ok {
			t.Errorf("request missing %q: %v", k, req)
		}
	}
	allowed := map[string]bool{}
	for _, k := range wantKeys {
		allowed[k] = true
	}
	for k := range req {
		if !allowed[k] {
			t.Errorf("unexpected request field %q", k)
		}
	}
	if !uuidV4.MatchString(fmt.Sprint(req["requestKey"])) {
		t.Errorf("requestKey %v is not a UUID v4", req["requestKey"])
	}
	checks := map[string]string{
		"deckId":          inst.ID,
		"title":           "WO-TEST ask aida",
		"group":           "vita-ehr",
		"path":            "/repos/vita-ehr-worktrees/feat", // the worktree, not ProjectPath
		"nativeSessionId": "0f5e2a4c-1b2d-4e3f-8a9b-0c1d2e3f4a5b",
		"tool":            "claude",
		"note":            "is it stuck?",
	}
	for k, want := range checks {
		if got := fmt.Sprint(req[k]); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestPatch19_RequestOmitsEmptyOptionalFields(t *testing.T) {
	stub := newAskAidaStub(t)
	stub.respond(t, `{"ok":true,"status":"accepted"}`)
	home, inst := armAskAidaHome(t)
	inst.ClaudeSessionID = ""

	pressAskAida(home)
	finish(t, home, submitAskAida(t, home, ""))
	req := stub.lastRequest(t)
	for _, k := range []string{"note", "nativeSessionId"} {
		if _, ok := req[k]; ok {
			t.Errorf("%q should be omitted when empty, got %v", k, req[k])
		}
	}
	if fmt.Sprint(req["path"]) != "/repos/vita-ehr" {
		t.Errorf("non-worktree path = %v, want ProjectPath", req["path"])
	}
}

// --- pending key ----------------------------------------------------------

// Every unconfirmed outcome keeps the key; the next B resends the SAME
// request (key and payload) without opening the dialog.
func TestPatch19_KeyReusedAfterUnconfirmedOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *askAidaStub)
	}{
		{"indeterminate", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"indeterminate","requestKey":"x"}`)
		}},
		{"timeout", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{}`)
			s.flag(t, "sleep")
			askAidaTimeout = 300 * time.Millisecond
		}},
		{"script error", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{}`)
			s.flag(t, "fail")
		}},
		{"script transport error", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":false,"error":"http_502","httpStatus":502}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newAskAidaStub(t)
			tc.setup(t, stub)
			home, inst := armAskAidaHome(t)

			pressAskAida(home)
			finish(t, home, submitAskAida(t, home, "first note"))
			if !strings.HasPrefix(statusText(home), "UNCONFIRMED — press B to retry the same ask (no new ring)") {
				t.Errorf("status = %q", statusText(home))
			}
			first := stub.lastRequest(t)
			if _, ok := home.askAidaPending[inst.ID]; !ok {
				t.Fatal("pending key was dropped after an unconfirmed outcome")
			}

			// The session's status drifts; the retry must not rebuild.
			inst.SetStatusThreadSafe(session.StatusWaiting)
			stub.respond(t, `{"ok":true,"status":"accepted","ref":"r9","busProject":"vita-ehr","busChannel":"build"}`)
			cmd := pressAskAida(home)
			if home.promptInputDialog.IsVisible() {
				t.Error("a retry must not reopen the dialog")
			}
			finish(t, home, cmd)
			second := stub.lastRequest(t)
			if fmt.Sprint(first) != fmt.Sprint(second) {
				t.Errorf("retry changed the request:\nfirst  %v\nsecond %v", first, second)
			}
			if _, ok := home.askAidaPending[inst.ID]; ok {
				t.Error("accepted must clear the pending key")
			}
		})
	}
}

func TestPatch19_KeyClearedAfterTerminalResults(t *testing.T) {
	terminal := []string{
		`{"ok":true,"status":"accepted","ref":"r1"}`,
		`{"ok":true,"status":"held","ref":"r1"}`,
		`{"ok":true,"status":"routed","ref":"r1"}`,
		`{"ok":true,"status":"refused","refusal":"budget_exhausted"}`,
		`{"ok":true,"status":"cooldown","ref":"r1","createdAt":"2026-09-26T18:00:00Z"}`,
		`{"ok":true,"status":"failed","error":"writes_disabled"}`,
		`{"ok":false,"error":"mcp_error:capability_denied"}`,
		`{"ok":false,"error":"mcp_error:idempotency_key_reused"}`,
	}
	for _, line := range terminal {
		t.Run(line, func(t *testing.T) {
			stub := newAskAidaStub(t)
			stub.respond(t, line)
			home, inst := armAskAidaHome(t)

			pressAskAida(home)
			finish(t, home, submitAskAida(t, home, ""))
			firstKey := fmt.Sprint(stub.lastRequest(t)["requestKey"])
			if _, ok := home.askAidaPending[inst.ID]; ok {
				t.Fatalf("pending key kept after %s", line)
			}

			// The next B is a new ask: dialog, fresh key.
			pressAskAida(home)
			finish(t, home, submitAskAida(t, home, ""))
			if got := fmt.Sprint(stub.lastRequest(t)["requestKey"]); got == firstKey {
				t.Errorf("a new ask reused the finished key %s", got)
			}
		})
	}
}

func TestPatch19_KeptOnFailedWithoutErrorCodeAndSetupErrors(t *testing.T) {
	for _, line := range []string{
		`{"ok":true,"status":"failed"}`,
		`{"ok":false,"error":"not_configured"}`,
		`{"ok":false,"error":"bad_env_file"}`,
		`{"ok":false,"error":"mcp_error:operation_unavailable"}`,
	} {
		stub := newAskAidaStub(t)
		stub.respond(t, line)
		home, inst := armAskAidaHome(t)
		pressAskAida(home)
		finish(t, home, submitAskAida(t, home, ""))
		if _, ok := home.askAidaPending[inst.ID]; !ok {
			t.Errorf("pending key dropped after %s", line)
		}
	}
}

func TestPatch19_InProgressClearsKeySoTheNextPressIsFresh(t *testing.T) {
	stub := newAskAidaStub(t)
	stub.respond(t, `{"ok":true,"status":"in_progress","requestKey":"11111111-2222-4333-8444-555555555555","createdAt":"2026-09-26T18:00:00Z"}`)
	home, inst := armAskAidaHome(t)

	pressAskAida(home)
	finish(t, home, submitAskAida(t, home, ""))
	if got := statusText(home); got != "Aida is still being asked about this session — press B in a moment to see the result" {
		t.Errorf("status = %q", got)
	}
	if p, ok := home.askAidaPending[inst.ID]; ok {
		t.Fatalf("in_progress must not keep or adopt a key, pending = %+v", p)
	}
	// The next press opens the dialog and sends a NEW key, never the hub's.
	stub.respond(t, `{"ok":true,"status":"accepted","ref":"r2"}`)
	pressAskAida(home)
	finish(t, home, submitAskAida(t, home, ""))
	if got := fmt.Sprint(stub.lastRequest(t)["requestKey"]); got == "11111111-2222-4333-8444-555555555555" {
		t.Errorf("next press reused the hub's in-flight key %s", got)
	}
}

func TestPatch19_SecondPressWhileInFlightDoesNotSend(t *testing.T) {
	stub := newAskAidaStub(t)
	stub.respond(t, `{"ok":true,"status":"accepted"}`)
	home, _ := armAskAidaHome(t)
	pressAskAida(home)
	cmd := submitAskAida(t, home, "")
	if again := pressAskAida(home); again != nil {
		t.Error("a press while the first ask is running must not start another")
	}
	finish(t, home, cmd)
	if stub.runs(t) != 1 {
		t.Errorf("script ran %d times, want 1", stub.runs(t))
	}
}

// --- status text ----------------------------------------------------------

func TestPatch19_StatusTextTable(t *testing.T) {
	now := time.Date(2026, 9, 26, 18, 7, 30, 0, time.UTC)
	ok := func(r askAidaResult) askAidaResultMsg { r.OK = true; return askAidaResultMsg{result: r} }
	cases := []struct {
		name string
		msg  askAidaResultMsg
		want string
	}{
		{"accepted", ok(askAidaResult{Status: "accepted", Ref: "ring-1", BusProject: "vita-ehr", BusChannel: "build"}),
			"Aida notified → vita-ehr/#build — ref ring-1"},
		{"accepted fallback", ok(askAidaResult{Status: "accepted", Ref: "ring-1", BusProject: "devy", BusChannel: "#aida-ops", Fallback: true}),
			"Aida notified → devy/#aida-ops — ref ring-1 (no project channel — sent to #aida-ops)"},
		{"held", ok(askAidaResult{Status: "held", Ref: "ring-2"}), "Held until 7 AM PT — ref ring-2"},
		{"routed", ok(askAidaResult{Status: "routed", Ref: "ring-3"}), "Logged, not delivered — ref ring-3"},
		{"refused", ok(askAidaResult{Status: "refused", Refusal: "sender_budget_exhausted"}), "NOT notified: sender_budget_exhausted"},
		{"cooldown", ok(askAidaResult{Status: "cooldown", Ref: "ring-4", CreatedAt: "2026-09-26T18:03:00.000Z"}), "Already asked 4 min ago — ref ring-4"},
		{"in_progress", ok(askAidaResult{Status: "in_progress", RequestKey: "k"}), "Aida is still being asked about this session — press B in a moment to see the result"},
		{"indeterminate", ok(askAidaResult{Status: "indeterminate"}), "UNCONFIRMED — press B to retry the same ask (no new ring)"},
		{"timeout", askAidaResultMsg{timedOut: true}, "UNCONFIRMED — press B to retry the same ask (no new ring)"},
		{"script error", askAidaResultMsg{runErr: fmt.Errorf("exit status 1")}, "UNCONFIRMED — press B to retry the same ask (no new ring)"},
		{"failed", ok(askAidaResult{Status: "failed", Error: "writes_disabled"}), "NOT notified: writes_disabled"},
		{"failed fallback", ok(askAidaResult{Status: "failed", Error: "aida_request_confirmation_missing", Fallback: true}),
			"NOT notified: aida_request_confirmation_missing (no project channel — sent to #aida-ops)"},
		{"script missing", askAidaResultMsg{notSetUp: "ask-aida.sh not found at /x"}, "Ask Aida not set up: ask-aida.sh not found at /x"},
		{"not configured", askAidaResultMsg{result: askAidaResult{Error: "not_configured"}}, "Ask Aida not set up: not_configured"},
		{"bad env file", askAidaResultMsg{result: askAidaResult{Error: "bad_env_file"}}, "Ask Aida not set up: bad_env_file"},
		{"tool missing", askAidaResultMsg{result: askAidaResult{Error: "mcp_error:rpc_-32602"}}, "NOT notified: mcp_error:rpc_-32602"},
		{"http error", askAidaResultMsg{result: askAidaResult{Error: "http_403", HTTPStatus: "403"}},
			"UNCONFIRMED — press B to retry the same ask (no new ring) (http_403)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := askAidaOutcome(tc.msg, now)
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
	// Only "accepted" may say Aida was notified.
	for _, st := range []string{"held", "routed", "refused", "cooldown", "in_progress", "indeterminate", "failed", "weird"} {
		got, _ := askAidaOutcome(ok(askAidaResult{Status: askAidaText(st), Error: "e"}), now)
		if strings.Contains(got, "notified →") || strings.HasPrefix(got, "Aida notified") {
			t.Errorf("%s must not claim Aida was notified: %q", st, got)
		}
	}
}

// A mistyped field in the hub's reply (0/1 fallback, numeric ids, epoch
// createdAt) must not turn a delivered ring into UNCONFIRMED.
func TestPatch19_LooseResultDecoding(t *testing.T) {
	stub := newAskAidaStub(t)
	stub.respond(t, `{"ok":true,"status":"accepted","ref":"ring-9","busProject":"devy","busChannel":"aida-ops","fallback":1,"busMessageId":"77","replayed":1,"ringStatus":{"x":1},"createdAt":1727373600000}`)
	home, inst := armAskAidaHome(t)
	pressAskAida(home)
	finish(t, home, submitAskAida(t, home, ""))
	if got := statusText(home); got != "Aida notified → devy/#aida-ops — ref ring-9 (no project channel — sent to #aida-ops)" {
		t.Errorf("status = %q", got)
	}
	if _, ok := home.askAidaPending[inst.ID]; ok {
		t.Error("a decoded accepted ring must clear the key")
	}
	// A non-string createdAt on cooldown falls back to "recently".
	res, ok := parseAskAidaOutput([]byte(`{"ok":true,"status":"cooldown","ref":7,"createdAt":1727373600000,"fallback":"true"}`))
	if !ok {
		t.Fatal("loose line did not parse")
	}
	got, _ := askAidaOutcome(askAidaResultMsg{result: res}, time.Now())
	if got != "Already asked recently — ref 7 (no project channel — sent to #aida-ops)" {
		t.Errorf("cooldown with numeric createdAt = %q", got)
	}
}

func TestPatch19_ResultReachesStatusBarThroughUpdate(t *testing.T) {
	stub := newAskAidaStub(t)
	stub.respond(t, `{"ok":true,"status":"held","ref":"ring-7","fallback":true}`)
	home, _ := armAskAidaHome(t)
	pressAskAida(home)
	finish(t, home, submitAskAida(t, home, ""))
	if got := statusText(home); got != "Held until 7 AM PT — ref ring-7 (no project channel — sent to #aida-ops)" {
		t.Errorf("status = %q", got)
	}
}

// Patch 21: when was Aida last asked about this session.
//
// A `B` ask that reached the doorbell (hub status accepted / held / routed)
// leaves a durable record in tool_data.last_aida_ask. The preview shows it
// under the Deck ID line, and the `B` dialog warns before a second ask. Every
// other outcome — refused, cooldown, in_progress, indeterminate, failed, a
// timeout, a script or setup error — leaves no record.
//
// Scripts are patch 19's stub (askAidaStub); the DB is a temp statedb swapped
// in as the global. Nothing reaches ~/.agent-deck or the network.
package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// withAskAidaDB swaps a temp statedb in as the global and seeds a row for
// inst, so a targeted UPDATE has a row to land on (an UPDATE on a missing row
// "succeeds" silently and would make the negative tests vacuous).
func withAskAidaDB(t *testing.T, inst *session.Instance) *statedb.StateDB {
	t.Helper()
	db, err := statedb.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("statedb.Open: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	prev := statedb.GetGlobal()
	statedb.SetGlobal(db)
	t.Cleanup(func() {
		statedb.SetGlobal(prev)
		_ = db.Close()
	})
	row := &statedb.InstanceRow{
		ID:          inst.ID,
		Title:       inst.Title,
		ProjectPath: inst.ProjectPath,
		GroupPath:   inst.GroupPath,
		Tool:        inst.Tool,
		Status:      "idle",
		CreatedAt:   time.Now(),
		ToolData:    json.RawMessage(`{"claude_session_id":"abc","notes":"keep me"}`),
	}
	if err := db.SaveInstance(row); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	return db
}

// dbToolData returns the instance's tool_data blob as a key map.
func dbToolData(t *testing.T, db *statedb.StateDB, id string) map[string]json.RawMessage {
	t.Helper()
	rows, err := db.LoadInstances()
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	for _, r := range rows {
		if r.ID == id {
			m := map[string]json.RawMessage{}
			if err := json.Unmarshal(r.ToolData, &m); err != nil {
				t.Fatalf("tool_data is not an object: %v (%s)", err, r.ToolData)
			}
			return m
		}
	}
	t.Fatalf("row %q not found", id)
	return nil
}

// finishAndRun is finish() plus running the Cmd Update returns, which is
// where patch 21's DB write lives.
func finishAndRun(t *testing.T, h *Home, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a script run, got no Cmd (status: %v)", h.err)
	}
	msg, ok := cmd().(askAidaResultMsg)
	if !ok {
		t.Fatalf("script Cmd returned %T, want askAidaResultMsg", msg)
	}
	if _, next := h.updateInner(msg); next != nil {
		_ = next()
	}
}

func pinAskAidaNow(t *testing.T, at time.Time) {
	t.Helper()
	prev := askAidaNow
	askAidaNow = func() time.Time { return at }
	t.Cleanup(func() { askAidaNow = prev })
}

// --- record written only when the ask reached the doorbell ----------------

func TestPatch21_RecordWrittenForDeliveredStatuses(t *testing.T) {
	at := time.Date(2026, 9, 26, 18, 7, 30, 0, time.UTC)
	cases := []struct {
		status   string
		fallback bool
	}{
		{"accepted", false},
		{"held", false},
		{"routed", true},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			stub := newAskAidaStub(t)
			fb := "false"
			if tc.fallback {
				fb = "true"
			}
			stub.respond(t, `{"ok":true,"status":"`+tc.status+`","ref":"ring-1","busProject":"vita-ehr","busChannel":"build","fallback":`+fb+`}`)
			pinAskAidaNow(t, at)
			home, inst := armAskAidaHome(t)
			db := withAskAidaDB(t, inst)

			pressAskAida(home)
			finishAndRun(t, home, submitAskAida(t, home, ""))

			// In memory, immediately.
			rec, ok := inst.LastAidaAsk()
			if !ok {
				t.Fatalf("no in-memory record after %s", tc.status)
			}
			if rec.At.Unix() != at.Unix() || rec.Status != tc.status || rec.Ref != "ring-1" ||
				rec.Channel != "vita-ehr/#build" || rec.Fallback != tc.fallback {
				t.Errorf("in-memory record = %+v", rec)
			}

			// In the DB, as an object (not a JSON string), beside the
			// untouched neighbours.
			td := dbToolData(t, db, inst.ID)
			raw, ok := td["last_aida_ask"]
			if !ok {
				t.Fatalf("DB has no last_aida_ask after %s: %v", tc.status, td)
			}
			if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
				t.Fatalf("last_aida_ask stored as %s, want an object", raw)
			}
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			if obj["at"] != float64(at.Unix()) || obj["status"] != tc.status || obj["ref"] != "ring-1" ||
				obj["channel"] != "vita-ehr/#build" || obj["fallback"] != tc.fallback {
				t.Errorf("DB object = %v", obj)
			}
			for _, k := range []string{"claude_session_id", "notes"} {
				if _, ok := td[k]; !ok {
					t.Errorf("targeted write dropped %q", k)
				}
			}
		})
	}
}

func TestPatch21_NoRecordForOtherOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *askAidaStub)
	}{
		{"refused", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"refused","refusal":"budget_exhausted","ref":"r"}`)
		}},
		{"cooldown", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"cooldown","ref":"r","createdAt":"2026-09-26T18:00:00Z"}`)
		}},
		{"in_progress", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"in_progress","requestKey":"k"}`)
		}},
		{"indeterminate", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"indeterminate","ref":"r"}`)
		}},
		{"failed", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"failed","error":"writes_disabled"}`)
		}},
		{"failed without code", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":true,"status":"failed"}`)
		}},
		{"not configured", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":false,"error":"not_configured"}`)
		}},
		{"mcp error", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":false,"error":"mcp_error:capability_denied"}`)
		}},
		{"ok false with accepted status", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{"ok":false,"status":"accepted","error":"http_502"}`)
		}},
		{"script error", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{}`)
			s.flag(t, "fail")
		}},
		{"timeout", func(t *testing.T, s *askAidaStub) {
			s.respond(t, `{}`)
			s.flag(t, "sleep")
			askAidaTimeout = 300 * time.Millisecond
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newAskAidaStub(t)
			tc.setup(t, stub)
			home, inst := armAskAidaHome(t)
			db := withAskAidaDB(t, inst)

			pressAskAida(home)
			finishAndRun(t, home, submitAskAida(t, home, ""))

			if rec, ok := inst.LastAidaAsk(); ok {
				t.Errorf("%s left an in-memory record: %+v", tc.name, rec)
			}
			if raw, ok := dbToolData(t, db, inst.ID)["last_aida_ask"]; ok {
				t.Errorf("%s wrote last_aida_ask to the DB: %s", tc.name, raw)
			}
		})
	}
}

// A new delivered ask replaces the record rather than keeping the old one.
func TestPatch21_SecondAskReplacesRecord(t *testing.T) {
	stub := newAskAidaStub(t)
	home, inst := armAskAidaHome(t)
	db := withAskAidaDB(t, inst)

	first := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, first)
	stub.respond(t, `{"ok":true,"status":"held","ref":"ring-1"}`)
	pressAskAida(home)
	finishAndRun(t, home, submitAskAida(t, home, ""))

	second := first.Add(3 * time.Hour)
	pinAskAidaNow(t, second)
	stub.respond(t, `{"ok":true,"status":"accepted","ref":"ring-2","busProject":"devy","busChannel":"aida-ops","fallback":true}`)
	pressAskAida(home)
	finishAndRun(t, home, submitAskAida(t, home, ""))

	rec, ok := inst.LastAidaAsk()
	if !ok || rec.Status != "accepted" || rec.Ref != "ring-2" || rec.At.Unix() != second.Unix() || !rec.Fallback {
		t.Fatalf("record after second ask = %+v (ok=%v)", rec, ok)
	}
	var obj map[string]any
	_ = json.Unmarshal(dbToolData(t, db, inst.ID)["last_aida_ask"], &obj)
	if obj["ref"] != "ring-2" {
		t.Errorf("DB record not replaced: %v", obj)
	}
}

// --- preview line ----------------------------------------------------------

func TestPatch21_PreviewLineText(t *testing.T) {
	at := time.Now().Add(-25 * time.Minute).Truncate(time.Second)
	stamp := formatActivityStamp(at, false)
	cases := []struct {
		status   string
		fallback bool
		want     string
	}{
		// Patch 24: accepted/held asks with a ref now also say they are
		// waiting for a reply (patch24_aida_answered_test.go covers the rest).
		{"accepted", false, "🔔 asked Aida: " + stamp + " — notified · waiting for reply"},
		{"held", false, "🔔 asked Aida: " + stamp + " — held until 7 AM · waiting for reply"},
		{"routed", false, "🔔 asked Aida: " + stamp + " — logged, not delivered"},
		{"accepted", true, "🔔 asked Aida: " + stamp + " — notified · waiting for reply · #aida-ops fallback"},
		{"held", true, "🔔 asked Aida: " + stamp + " — held until 7 AM · waiting for reply · #aida-ops fallback"},
	}
	for _, tc := range cases {
		inst := session.NewInstanceWithTool("p21", "/tmp", "claude")
		inst.SetLastAidaAsk(session.NewAidaAsk(at, tc.status, "ring-1", "vita-ehr/#build", tc.fallback))
		if got := lastAidaAskPreviewLine(inst); got != tc.want {
			t.Errorf("%s fallback=%v:\n got  %q\n want %q", tc.status, tc.fallback, got, tc.want)
		}
	}

	if got := lastAidaAskPreviewLine(session.NewInstanceWithTool("none", "/tmp", "claude")); got != "" {
		t.Errorf("no record should hide the line, got %q", got)
	}
	if got := lastAidaAskPreviewLine(nil); got != "" {
		t.Errorf("nil instance should hide the line, got %q", got)
	}
}

func TestPatch21_PreviewPaneShowsLineUnderDeckID(t *testing.T) {
	home, inst := armAskAidaHome(t)

	without := home.renderPreviewPane(120, 60)
	if strings.Contains(without, "asked Aida:") {
		t.Fatalf("preview shows an ask line with no record:\n%s", without)
	}

	inst.SetLastAidaAsk(session.NewAidaAsk(time.Now().Add(-5*time.Minute), "accepted", "ring-1", "vita-ehr/#build", false))
	with := home.renderPreviewPane(120, 60)
	deck := strings.Index(with, "🆔 Deck ID:")
	ask := strings.Index(with, "🔔 asked Aida:")
	if deck < 0 || ask < 0 {
		t.Fatalf("preview missing Deck ID (%d) or ask line (%d):\n%s", deck, ask, with)
	}
	if ask < deck {
		t.Errorf("ask line should sit under the Deck ID line")
	}
	if !strings.Contains(with, "— notified") {
		t.Errorf("ask line missing its word:\n%s", with)
	}
}

// --- dialog warning --------------------------------------------------------

func TestPatch21_DialogWarnsWhenAlreadyAsked(t *testing.T) {
	newAskAidaStub(t)
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, inst := armAskAidaHome(t)
	inst.SetLastAidaAsk(session.NewAidaAsk(now.Add(-10*time.Minute), "held", "ring-1", "", false))

	// Patch 24: an unanswered held ask adds ", no reply yet".
	want := "Already asked Aida " + humanizeSince(10*time.Minute) + " (held until 7 AM), no reply yet — Enter asks again"
	if got := askAidaAlreadyAskedLine(inst, now); got != want {
		t.Errorf("warning line:\n got  %q\n want %q", got, want)
	}

	pressAskAida(home)
	if !home.promptInputDialog.IsAskAida() {
		t.Fatal("dialog did not open")
	}
	view := home.promptInputDialog.View("")
	if !strings.Contains(view, want) {
		t.Errorf("dialog missing the warning line:\n%s", view)
	}
	// Above the input: the warning comes before the hint line.
	if strings.Index(view, want) > strings.Index(view, askAidaHint) {
		t.Errorf("warning should sit above the input/hint:\n%s", view)
	}

	// The prompt-session dialog never inherits it.
	home.promptInputDialog.Show(inst.ID, inst.Title)
	if strings.Contains(home.promptInputDialog.View(""), "Already asked Aida") {
		t.Error("Show must clear the Ask Aida warning")
	}
}

func TestPatch21_DialogUnchangedWithoutRecord(t *testing.T) {
	newAskAidaStub(t)
	home, inst := armAskAidaHome(t)
	if got := askAidaAlreadyAskedLine(inst, time.Now()); got != "" {
		t.Errorf("no record should produce no warning, got %q", got)
	}
	pressAskAida(home)
	if !home.promptInputDialog.IsAskAida() {
		t.Fatal("dialog did not open")
	}
	if view := home.promptInputDialog.View(""); strings.Contains(view, "Already asked Aida") {
		t.Errorf("dialog shows a warning with no record:\n%s", view)
	}
}

func TestPatch21_WordTable(t *testing.T) {
	for status, want := range map[string]string{
		"accepted": "notified",
		"held":     "held until 7 AM",
		"routed":   "logged, not delivered",
	} {
		if got := aidaAskWord(status); got != want {
			t.Errorf("aidaAskWord(%q) = %q, want %q", status, got, want)
		}
	}
}

// askAidaRecordFor is the one gate: pinned directly so a refactor of
// askAidaOutcome's key actions cannot quietly widen it.
func TestPatch21_RecordGate(t *testing.T) {
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	ok := func(r askAidaResult) askAidaResultMsg { r.OK = true; return askAidaResultMsg{result: r} }
	for _, st := range []string{"accepted", "held", "routed"} {
		rec, got := askAidaRecordFor(ok(askAidaResult{Status: askAidaText(st), Ref: "r", BusProject: "p", BusChannel: "#c"}), now)
		if !got || rec.Status != st || rec.Channel != "p/#c" || !rec.At.Equal(now) {
			t.Errorf("%s: got %+v ok=%v", st, rec, got)
		}
	}
	// No channel in the reply stores no channel (not askAidaChannelLabel's "Aida").
	if rec, _ := askAidaRecordFor(ok(askAidaResult{Status: "accepted"}), now); rec.Channel != "" {
		t.Errorf("empty channel stored as %q", rec.Channel)
	}
	for _, msg := range []askAidaResultMsg{
		ok(askAidaResult{Status: "refused"}),
		ok(askAidaResult{Status: "cooldown"}),
		ok(askAidaResult{Status: "in_progress"}),
		ok(askAidaResult{Status: "indeterminate"}),
		ok(askAidaResult{Status: "failed", Error: "x"}),
		ok(askAidaResult{Status: "weird"}),
		{result: askAidaResult{Status: "accepted"}}, // ok:false
		{notSetUp: "missing", result: askAidaResult{OK: true, Status: "accepted"}},
		{timedOut: true, result: askAidaResult{OK: true, Status: "accepted"}},
	} {
		if _, got := askAidaRecordFor(msg, now); got {
			t.Errorf("recorded for %+v", msg)
		}
	}
}

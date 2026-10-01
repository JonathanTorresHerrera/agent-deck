// Patch 29: clear the Aida bell by hand (Ctrl+X in the B dialog), and the
// preview/dialog wording for a bell cleared that way.
package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch29_CtrlXOnlyWhenClearable(t *testing.T) {
	newAskAidaStub(t)
	now := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, inst := armAskAidaHome(t)

	// No record: Ctrl+X is a plain textinput key, the dialog stays open.
	pressAskAida(home)
	if strings.Contains(home.promptInputDialog.View(""), "Ctrl+X") {
		t.Error("hint offers Ctrl+X without a bell")
	}
	if _, cmd := home.promptInputDialog.Update(tea.KeyMsg{Type: tea.KeyCtrlX}); cmd != nil {
		if _, isClear := cmd().(aidaBellClearMsg); isClear {
			t.Fatal("Ctrl+X emitted a clear without a bell")
		}
	}
	if !home.promptInputDialog.IsAskAida() {
		t.Fatal("dialog closed on Ctrl+X without a bell")
	}
	home.promptInputDialog.Hide()

	// A waiting bell: the hint offers it, Ctrl+X emits the clear with the note.
	inst.SetLastAidaAsk(session.NewAidaAsk(now.Add(-time.Hour), "accepted", "doorbell-1", "", false))
	pressAskAida(home)
	if !strings.Contains(home.promptInputDialog.View(""), "Ctrl+X mark handled") {
		t.Errorf("hint missing Ctrl+X:\n%s", home.promptInputDialog.View(""))
	}
	typeInto(home.promptInputDialog, "  looked at it  ")
	_, cmd := home.promptInputDialog.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if cmd == nil {
		t.Fatal("Ctrl+X returned no Cmd")
	}
	msg, ok := cmd().(aidaBellClearMsg)
	if !ok || msg.instanceID != inst.ID || msg.note != "looked at it" {
		t.Fatalf("clear msg = %#v", msg)
	}
	if home.promptInputDialog.IsVisible() {
		t.Error("dialog still open after Ctrl+X")
	}

	// Already answered: not clearable, the hint goes back to normal.
	inst.SetLastAidaAsk(session.NewAidaAsk(now.Add(-time.Hour), "accepted", "doorbell-1", "", false).WithAnswered(now))
	pressAskAida(home)
	if strings.Contains(home.promptInputDialog.View(""), "Ctrl+X") {
		t.Error("hint offers Ctrl+X on an answered ask")
	}
}

func TestPatch29_ClearMarksHandledAndPersists(t *testing.T) {
	newAskAidaStub(t)
	now := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	pinAskAidaNow(t, now)
	home, inst := armAskAidaHome(t)
	db := withAskAidaDB(t, inst)
	withExtras, ok := session.ParseAidaAsk(json.RawMessage(
		`{"at":` + jsonInt(now.Add(-3*time.Hour).Unix()) + `,"status":"held","ref":"doorbell-9","channel":"devy/#aida-ops","fallback":false,"future":1}`))
	if !ok {
		t.Fatal("fixture did not parse")
	}
	inst.SetLastAidaAsk(withExtras)
	home.refreshSessionRenderSnapshot(nil)
	if !strings.Contains(renderAidaRow(t, inst, 140), "🔔") {
		t.Fatal("fixture row has no bell")
	}

	persist := home.handleAidaBellClear(aidaBellClearMsg{instanceID: inst.ID, note: "done by hand"})
	if persist == nil {
		t.Fatal("no persist Cmd")
	}
	_ = persist()

	rec, _ := inst.LastAidaAsk()
	if !rec.IsCleared() || rec.ClearedBy != "jt" || rec.ClearReason != "manual" || rec.Note != "done by hand" || !rec.AnsweredAt.Equal(now) {
		t.Fatalf("record = %+v", rec)
	}
	var obj map[string]any
	if err := json.Unmarshal(dbToolData(t, db, inst.ID)["last_aida_ask"], &obj); err != nil {
		t.Fatal(err)
	}
	if obj["answered_at"] != float64(now.Unix()) || obj["cleared_by"] != "jt" || obj["clear_reason"] != "manual" || obj["note"] != "done by hand" || obj["future"] != float64(1) {
		t.Errorf("DB record = %v", obj)
	}
	snap := home.getSessionRenderSnapshot()[inst.ID]
	if !snap.aidaAsk.IsCleared() {
		t.Error("render snapshot not patched")
	}
	if !strings.Contains(errText(home), "Marked handled") {
		t.Errorf("status = %q", errText(home))
	}
	if strings.Contains(renderAidaRow(t, inst, 140), "🔔") {
		t.Error("row still shows the bell after the clear")
	}

	// A second clear is a no-op with a clear message.
	if home.handleAidaBellClear(aidaBellClearMsg{instanceID: inst.ID}) != nil {
		t.Error("second clear persisted again")
	}
	if !strings.Contains(errText(home), "Nothing to clear") {
		t.Errorf("status = %q", errText(home))
	}
	// Answered bells are never polled again.
	if len(aidaStatusCandidates([]aidaAskEntry{{id: inst.ID, ask: rec}}, now)) != 0 {
		t.Error("cleared ask is still a poll candidate")
	}
}

func TestPatch29_Wording(t *testing.T) {
	now := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	at := now.Add(-2 * time.Hour)
	cleared := session.NewAidaAsk(at, "accepted", "doorbell-1", "", false).WithCleared(now.Add(-10*time.Minute), "aida", "message", "")
	inst := session.NewInstanceWithTool("p27", "/tmp", "claude")
	inst.SetLastAidaAsk(cleared)

	want := "Marked handled by aida " + humanizeSince(10*time.Minute) + " — Enter asks again"
	if got := askAidaAlreadyAskedLine(inst, now); got != want {
		t.Errorf("dialog:\n got  %q\n want %q", got, want)
	}
	line := lastAidaAskPreviewLine(inst)
	if !strings.HasPrefix(line, "✅ Marked handled by aida: ") || strings.Contains(line, "Aida answered") {
		t.Errorf("preview = %q", line)
	}
	inst.SetLastAidaAsk(cleared.WithCleared(now, "jt", "manual", "checked, fine"))
	if line := lastAidaAskPreviewLine(inst); !strings.HasSuffix(line, " — checked, fine") {
		t.Errorf("preview note = %q", line)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

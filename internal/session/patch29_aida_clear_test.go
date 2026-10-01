// Patch 29: a bell cleared by hand keeps answered_at semantics and adds
// cleared_by / clear_reason / note.
package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPatch29_WithClearedRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	base, ok := ParseAidaAsk(json.RawMessage(`{"at":1790000000,"status":"accepted","ref":"doorbell-1","channel":"devy/#aida-ops","fallback":false,"future":{"x":1}}`))
	if !ok {
		t.Fatal("fixture")
	}
	c := base.WithCleared(at, " aida ", "message", "  "+strings.Repeat("n", AidaAskNoteMax+20))
	if !c.IsCleared() || !c.IsAnswered() || c.ClearedBy != "aida" || c.ClearReason != "message" {
		t.Fatalf("cleared = %+v", c)
	}
	if len([]rune(c.Note)) != AidaAskNoteMax {
		t.Errorf("note not bounded: %d", len([]rune(c.Note)))
	}
	var obj map[string]any
	if err := json.Unmarshal(c.JSON(), &obj); err != nil {
		t.Fatal(err)
	}
	if obj["answered_at"] != float64(at.Unix()) || obj["cleared_by"] != "aida" || obj["clear_reason"] != "message" || obj["future"] == nil {
		t.Errorf("JSON = %v", obj)
	}
	back, ok := ParseAidaAsk(c.JSON())
	if !ok || !back.IsCleared() || back.ClearedBy != "aida" || back.Note != c.Note || !back.AnsweredAt.Equal(at) {
		t.Errorf("round trip = %+v", back)
	}
	// An ack seen on the hub is answered but not cleared.
	if base.WithAnswered(at).IsCleared() {
		t.Error("hub ack reads as a manual clear")
	}
	// Wrong-typed keys read as empty, never fail the record.
	odd, ok := ParseAidaAsk(json.RawMessage(`{"at":1790000000,"status":"accepted","ref":"r","cleared_by":7,"note":{"a":1}}`))
	if !ok || odd.ClearedBy != "" || odd.Note != "" {
		t.Errorf("odd = %+v ok=%v", odd, ok)
	}
}

func TestPatch29_ClearLastAidaAskGuards(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	inst := NewInstanceWithTool("p27", "/tmp", "claude")
	if _, ok := inst.ClearLastAidaAsk("doorbell-1", now, "jt", "manual", ""); ok {
		t.Error("cleared with no record")
	}
	inst.SetLastAidaAsk(NewAidaAsk(now.Add(-time.Hour), "accepted", "doorbell-1", "", false))
	if _, ok := inst.ClearLastAidaAsk("doorbell-2", now, "jt", "manual", ""); ok {
		t.Error("cleared a different ref")
	}
	out, ok := inst.ClearLastAidaAsk("doorbell-1", now, "jt", "manual", "x")
	if !ok || out == nil {
		t.Fatal("did not clear")
	}
	if rec, _ := inst.LastAidaAsk(); !rec.IsCleared() || rec.Note != "x" {
		t.Errorf("in memory = %+v", rec)
	}
	if _, ok := inst.ClearLastAidaAsk("doorbell-1", now, "jt", "manual", ""); ok {
		t.Error("cleared twice")
	}
}

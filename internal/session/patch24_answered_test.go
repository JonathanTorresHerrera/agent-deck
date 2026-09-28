// Patch 24: last_aida_ask.answered_at — when the deck saw Aida answer the
// ring (ask-aida.sh --status reported acked:true).
package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPatch24_AnsweredParse(t *testing.T) {
	a, ok := ParseAidaAsk(json.RawMessage(aidaAskWithExtras))
	if !ok {
		t.Fatal("fixture did not parse")
	}
	at, answered := a.Answered()
	if !answered || at.Unix() != 1790003600 {
		t.Errorf("Answered() = %v, %v; want 1790003600, true", at, answered)
	}

	fresh := NewAidaAsk(time.Unix(1790000000, 0), "accepted", "doorbell-1", "", false)
	if _, answered := fresh.Answered(); answered {
		t.Error("a fresh ask must not be answered")
	}
	if m := decodeObj(t, fresh.JSON()); m["answered_at"] != nil {
		t.Errorf("fresh JSON carries answered_at: %v", m)
	}
}

// A malformed answered_at is "unanswered", never "no record".
func TestPatch24_AnsweredLenient(t *testing.T) {
	for _, v := range []string{`"soon"`, `0`, `-5`, `null`, `1.5`, `{"x":1}`} {
		raw := `{"at":1790000000,"status":"accepted","ref":"doorbell-1","answered_at":` + v + `}`
		a, ok := ParseAidaAsk(json.RawMessage(raw))
		if !ok {
			t.Errorf("answered_at=%s dropped the whole record", v)
			continue
		}
		if _, answered := a.Answered(); answered {
			t.Errorf("answered_at=%s read as answered", v)
		}
		// Writing it back leaves the odd value alone.
		if got := string(decodeRaw(t, a.JSON())["answered_at"]); got != v {
			t.Errorf("answered_at=%s rewritten as %s", v, got)
		}
	}
}

func TestPatch24_WithAnsweredKeepsUnknownFields(t *testing.T) {
	raw := `{"at":1790000000,"status":"held","ref":"doorbell-1","channel":"c","fallback":true,"future":{"x":1}}`
	a, _ := ParseAidaAsk(json.RawMessage(raw))
	out := a.WithAnswered(time.Unix(1790007200, 0)).JSON()
	m := decodeObj(t, out)
	if m["answered_at"] != float64(1790007200) {
		t.Errorf("answered_at = %v", m["answered_at"])
	}
	if m["at"] != float64(1790000000) || m["status"] != "held" || m["ref"] != "doorbell-1" ||
		m["channel"] != "c" || m["fallback"] != true {
		t.Errorf("known fields changed: %v", m)
	}
	if f, ok := m["future"].(map[string]any); !ok || f["x"] != float64(1) {
		t.Errorf("unknown field lost: %v", m)
	}
}

func TestPatch24_AwaitingReply(t *testing.T) {
	now := time.Unix(1790000000, 0)
	mk := func(status, ref string, age time.Duration) AidaAsk {
		return NewAidaAsk(now.Add(-age), status, ref, "", false)
	}
	cases := []struct {
		name string
		a    AidaAsk
		want bool
	}{
		{"accepted", mk("accepted", "doorbell-1", time.Hour), true},
		{"held", mk("held", "doorbell-1", time.Hour), true},
		{"routed", mk("routed", "doorbell-1", time.Hour), false},
		{"no ref", mk("accepted", "  ", time.Hour), false},
		{"47h", mk("accepted", "doorbell-1", 47*time.Hour), true},
		{"49h", mk("accepted", "doorbell-1", 49*time.Hour), false},
		{"answered", mk("accepted", "doorbell-1", time.Hour).WithAnswered(now), false},
	}
	for _, tc := range cases {
		if got := tc.a.AwaitingReply(now); got != tc.want {
			t.Errorf("%s: AwaitingReply = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPatch24_MarkAnsweredChecksRef(t *testing.T) {
	inst := NewInstanceWithTool("p24", "/tmp", "claude")
	now := time.Unix(1790000000, 0)
	if _, ok := inst.MarkLastAidaAskAnswered("doorbell-1", now); ok {
		t.Error("marked answered with no record")
	}
	inst.SetLastAidaAsk(NewAidaAsk(now.Add(-time.Hour), "accepted", "doorbell-2", "", false))
	if _, ok := inst.MarkLastAidaAskAnswered("doorbell-1", now); ok {
		t.Error("an old ring's ack marked a newer ask answered")
	}
	if rec, _ := inst.LastAidaAsk(); rec.IsAnswered() {
		t.Error("record changed on a ref mismatch")
	}
	out, ok := inst.MarkLastAidaAskAnswered("doorbell-2", now)
	if !ok {
		t.Fatal("matching ref not marked")
	}
	if decodeObj(t, out)["answered_at"] != float64(now.Unix()) {
		t.Errorf("persist bytes = %s", out)
	}
	rec, _ := inst.LastAidaAsk()
	if at, answered := rec.Answered(); !answered || !at.Equal(now) {
		t.Errorf("in-memory record = %+v", rec)
	}
	// A second ack is a no-op: the first answer time stands.
	if _, ok := inst.MarkLastAidaAskAnswered("doorbell-2", now.Add(time.Hour)); ok {
		t.Error("an answered record was re-marked")
	}
}

func decodeObj(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %s", raw)
	}
	return m
}

func decodeRaw(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %s", raw)
	}
	return m
}

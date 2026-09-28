// Patch 21: tool_data.last_aida_ask — the record of the last `B` ask that
// reached Aida's doorbell. These tests pin the object shape, that unknown
// fields inside it (a future "answered_at") survive every round trip, and
// that both legacy and current save paths keep the key.
package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const aidaAskWithExtras = `{"at":1790000000,"status":"held","ref":"ring-1","channel":"vita-ehr/#build","fallback":false,"answered_at":1790003600,"future":{"x":1}}`

func TestAidaAsk_JSONShape(t *testing.T) {
	at := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	raw := NewAidaAsk(at, "accepted", "ring-1", "vita-ehr/#build", true).JSON()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("JSON() is not an object: %v (%s)", err, raw)
	}
	want := map[string]any{
		"at": float64(at.Unix()), "status": "accepted", "ref": "ring-1",
		"channel": "vita-ehr/#build", "fallback": true,
	}
	for k, v := range want {
		if obj[k] != v {
			t.Errorf("%s = %v, want %v", k, obj[k], v)
		}
	}
	if len(obj) != len(want) {
		t.Errorf("unexpected keys in %s", raw)
	}

	got, ok := ParseAidaAsk(raw)
	if !ok || !got.At.Equal(at) || got.Status != "accepted" || got.Ref != "ring-1" ||
		got.Channel != "vita-ehr/#build" || !got.Fallback {
		t.Errorf("ParseAidaAsk round trip = %+v ok=%v", got, ok)
	}
}

func TestAidaAsk_UnknownFieldsSurvive(t *testing.T) {
	a, ok := ParseAidaAsk(json.RawMessage(aidaAskWithExtras))
	if !ok {
		t.Fatal("record with extra fields did not parse")
	}
	out := a.JSON()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if string(obj["answered_at"]) != "1790003600" || string(obj["future"]) != `{"x":1}` {
		t.Errorf("unknown fields lost on round trip: %s", out)
	}
	if string(obj["status"]) != `"held"` {
		t.Errorf("known field changed: %s", out)
	}
}

func TestAidaAsk_ParseRejectsJunk(t *testing.T) {
	for _, raw := range []string{"", "null", `"a string"`, "{}", `{"status":"accepted"}`, `{"at":0,"status":"held"}`, "[1]", "{broken"} {
		if _, ok := ParseAidaAsk(json.RawMessage(raw)); ok {
			t.Errorf("ParseAidaAsk(%q) = ok, want not ok", raw)
		}
	}
}

func TestAidaAsk_ToolDataHelpers(t *testing.T) {
	seeded := json.RawMessage(`{"claude_session_id":"abc","last_prompt_at":123,"notes":"keep me"}`)
	td := WriteLastAidaAskToToolData(seeded, json.RawMessage(aidaAskWithExtras))
	if got := ReadLastAidaAskFromToolData(td); !strings.Contains(string(got), `"answered_at":1790003600`) {
		t.Fatalf("read back %s", got)
	}
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(td, &m)
	for _, k := range []string{"claude_session_id", "last_prompt_at", "notes"} {
		if _, ok := m[k]; !ok {
			t.Errorf("dropped %q: %s", k, td)
		}
	}
	// No record removes the key — never null or {} — so a saver that has not
	// seen a record cannot wipe one another process just wrote.
	cleared := WriteLastAidaAskToToolData(td, nil)
	m2 := map[string]json.RawMessage{}
	_ = json.Unmarshal(cleared, &m2)
	if _, ok := m2["last_aida_ask"]; ok {
		t.Errorf("nil record should remove the key: %s", cleared)
	}
	if got := ReadLastAidaAskFromToolData(json.RawMessage(`{"notes":"x"}`)); got != nil {
		t.Errorf("legacy row read = %s, want nil", got)
	}
}

// A legacy binary rebuilds tool_data without the key; MergeToolDataExtras
// must carry it, unknown inner fields included.
func TestAidaAsk_LegacySaveKeepsKey(t *testing.T) {
	db := withTempGlobalStateDB(t)
	inst := NewInstanceWithTool("aida-legacy", "/tmp", "claude")
	seedInstanceRow(t, db, inst, `{"claude_session_id":"abc","last_aida_ask":`+aidaAskWithExtras+`}`)

	// The "legacy" save: a full row whose tool_data lacks the key.
	seedInstanceRow(t, db, inst, `{"claude_session_id":"abc"}`)

	rows, err := db.LoadInstances()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID != inst.ID {
			continue
		}
		raw := ReadLastAidaAskFromToolData(r.ToolData)
		if !strings.HasPrefix(string(raw), "{") || !strings.Contains(string(raw), `"answered_at":1790003600`) {
			t.Fatalf("legacy save lost the record: %s", r.ToolData)
		}
		return
	}
	t.Fatal("row not found")
}

// The current binary: targeted write, load, full save, reload. The object
// stays an object, unknown fields survive, and a stale snapshot that loaded
// before the ask cannot wipe it.
func TestAidaAsk_StorageRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storage := newTestStorage(t)
	db := storage.GetDB()

	inst := NewInstance("aida-roundtrip", "/tmp")
	inst.Tool = "shell"
	if err := storage.SaveWithGroups([]*Instance{inst}, NewGroupTreeWithGroups([]*Instance{inst}, nil)); err != nil {
		t.Fatal(err)
	}
	stale, _, err := storage.LoadWithGroups() // loaded before the ask
	if err != nil {
		t.Fatal(err)
	}

	if err := db.WriteLastAidaAsk(inst.ID, []byte(aidaAskWithExtras)); err != nil {
		t.Fatalf("WriteLastAidaAsk: %v", err)
	}
	readRaw := func() string {
		t.Helper()
		rows, err := db.LoadInstances()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == inst.ID {
				return string(ReadLastAidaAskFromToolData(r.ToolData))
			}
		}
		t.Fatal("row not found")
		return ""
	}
	if raw := readRaw(); !strings.HasPrefix(raw, "{") {
		t.Fatalf("targeted write stored %q, want an object", raw)
	}

	// A stale snapshot with no record saves over the row: the record stays.
	if err := storage.SaveWithGroups(stale, NewGroupTreeWithGroups(stale, nil)); err != nil {
		t.Fatal(err)
	}
	if raw := readRaw(); !strings.Contains(raw, `"answered_at":1790003600`) {
		t.Fatalf("stale save wiped the record: %q", raw)
	}

	// Load -> getter -> save -> reload keeps everything.
	loaded, _, err := storage.LoadWithGroups()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := loaded[0].LastAidaAsk()
	if !ok || rec.Status != "held" || rec.Ref != "ring-1" || rec.At.Unix() != 1790000000 {
		t.Fatalf("loaded record = %+v ok=%v", rec, ok)
	}
	if err := storage.SaveWithGroups(loaded, NewGroupTreeWithGroups(loaded, nil)); err != nil {
		t.Fatal(err)
	}
	raw := readRaw()
	if !strings.HasPrefix(raw, "{") || !strings.Contains(raw, `"answered_at":1790003600`) || !strings.Contains(raw, `"future":{"x":1}`) {
		t.Fatalf("full save lost unknown fields: %q", raw)
	}
}

func TestAidaAsk_InstanceGetterSetter(t *testing.T) {
	inst := NewInstance("aida-mem", "/tmp")
	if _, ok := inst.LastAidaAsk(); ok {
		t.Fatal("fresh instance has a record")
	}
	at := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)
	out := inst.SetLastAidaAsk(NewAidaAsk(at, "routed", "r", "", false))
	if !strings.HasPrefix(string(out), "{") {
		t.Fatalf("SetLastAidaAsk returned %q", out)
	}
	rec, ok := inst.LastAidaAsk()
	if !ok || rec.Status != "routed" || !rec.At.Equal(at) {
		t.Errorf("getter = %+v ok=%v", rec, ok)
	}
}

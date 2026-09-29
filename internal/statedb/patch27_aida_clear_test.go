package statedb

import (
	"encoding/json"
	"testing"
)

// Patch 27: the out-of-process clear lands only on the same, unanswered ask.
func TestPatch27_WriteLastAidaAskIfUnanswered(t *testing.T) {
	db := openTestDB(t)
	seedInstance(t, db, "s1", json.RawMessage(`{"last_aida_ask":{"at":1790000000,"status":"accepted","ref":"doorbell-1"},"other":"keep"}`))
	seedInstance(t, db, "s2", json.RawMessage(`{"last_aida_ask":{"at":1790000000,"status":"accepted","ref":"doorbell-2","answered_at":1790000100}}`))
	seedInstance(t, db, "s3", json.RawMessage(`{}`))
	cleared := []byte(`{"at":1790000000,"status":"accepted","ref":"doorbell-1","answered_at":1790000200,"cleared_by":"aida"}`)

	if ok, err := db.WriteLastAidaAskIfUnanswered("s1", "doorbell-OTHER", cleared); err != nil || ok {
		t.Fatalf("wrong ref: ok=%v err=%v", ok, err)
	}
	if ok, err := db.WriteLastAidaAskIfUnanswered("s1", "doorbell-1", cleared); err != nil || !ok {
		t.Fatalf("clear: ok=%v err=%v", ok, err)
	}
	td := readToolData(t, db, "s1")
	var rec map[string]any
	_ = json.Unmarshal(td["last_aida_ask"], &rec)
	if rec["cleared_by"] != "aida" || rec["answered_at"] != float64(1790000200) || string(td["other"]) != `"keep"` {
		t.Errorf("s1 = %v other=%s", rec, td["other"])
	}
	if ok, _ := db.WriteLastAidaAskIfUnanswered("s1", "doorbell-1", cleared); ok {
		t.Error("second clear wrote again")
	}
	if ok, _ := db.WriteLastAidaAskIfUnanswered("s2", "doorbell-2", cleared); ok {
		t.Error("overwrote an answered ask")
	}
	if ok, _ := db.WriteLastAidaAskIfUnanswered("s3", "doorbell-1", cleared); ok {
		t.Error("wrote a session with no ask")
	}
}

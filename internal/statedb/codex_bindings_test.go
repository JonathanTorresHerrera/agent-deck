package statedb

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

func TestCodexBindingRows(t *testing.T) {
	db := openTestDB(t)
	const thread = "01b00000-0000-7000-8000-00000000a001"
	seed := func(id, tool string, toolData string, archived bool) {
		t.Helper()
		now := time.Now()
		row := &InstanceRow{
			ID: id, Title: id, Status: "idle", Tool: tool, ProjectPath: "/work/" + id,
			CreatedAt: now, LastAccessed: now, GroupPath: "default",
			ToolData: json.RawMessage(toolData),
		}
		if archived {
			row.ArchivedAt = now
		}
		if err := db.SaveInstance(row); err != nil {
			t.Fatalf("SaveInstance %s: %v", id, err)
		}
	}
	seed("owner", "codex", `{"codex_session_id":"`+thread+`","additional_paths":["/work/extra"]}`, false)
	seed("archived", "codex", `{"codex_session_id":"`+thread+`"}`, true)
	seed("other", "codex", `{"codex_session_id":"01b00000-0000-7000-8000-00000000a002"}`, false)
	seed("env", "codex", `{}`, false)
	seed("numeric", "codex", `{"codex_session_id":42}`, false)
	if _, err := db.DB().Exec(`UPDATE instances SET tool_data = '{broken' WHERE id = 'other'`); err != nil {
		t.Fatal(err)
	}

	rows, err := db.CodexBindingRows(thread, "env")
	if err != nil {
		t.Fatalf("CodexBindingRows: %v", err)
	}
	byID := map[string]CodexBindingRow{}
	var ids []string
	for _, r := range rows {
		byID[r.ID] = r
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	if want := []string{"archived", "env", "owner"}; len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("rows = %v, want %v", ids, want)
	}
	if r := byID["owner"]; r.CodexSessionID != thread || r.Archived || r.ProjectPath != "/work/owner" || r.Tool != "codex" {
		t.Fatalf("owner row = %+v", r)
	}
	if !byID["archived"].Archived {
		t.Fatal("archived row must say so")
	}
	if r := byID["env"]; r.CodexSessionID != "" {
		t.Fatalf("unbound env row = %+v", r)
	}

	// No thread: only the env row, and a malformed row never fails the query.
	rows, err = db.CodexBindingRows("", "other")
	if err != nil {
		t.Fatalf("CodexBindingRows with malformed tool_data: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "other" || rows[0].CodexSessionID != "" {
		t.Fatalf("rows = %+v, want only the malformed env row with no binding", rows)
	}
}

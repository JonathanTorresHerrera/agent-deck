// Patch 25: `list --json` does not re-probe archived sessions.
//
// buildListJSON ran UpdateStatus on every row. A fresh CLI process has no
// recheck timestamps, so each session whose tmux pane is gone cost a direct
// tmux probe plus a pane-exit-status query: on a 366-row deck (most of them
// archived) `agent-deck list --json` took 49-64 s (measured 2026-09-28),
// stalling agent-deck-mcp and the bus postman. The TUI's background sweep
// already skips archived rows for this reason (shouldPollStatusInLoop).
package main

import (
	"encoding/json"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch25_ListJSONSkipsStatusProbeForArchived(t *testing.T) {
	live := session.NewInstanceWithTool("live", t.TempDir(), "claude")
	archived := session.NewInstanceWithTool("archived", t.TempDir(), "claude")
	archived.Status = session.StatusStopped
	archived.ArchivedAt = archived.CreatedAt

	var probed []string
	orig := listUpdateStatus
	listUpdateStatus = func(inst *session.Instance) { probed = append(probed, inst.Title) }
	defer func() { listUpdateStatus = orig }()

	out, err := buildListJSON("default", []*session.Instance{live, archived})
	if err != nil {
		t.Fatal(err)
	}
	if len(probed) != 1 || probed[0] != "live" {
		t.Fatalf("status probed for %v, want only [live]", probed)
	}

	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want both sessions still listed", len(rows))
	}
	for _, r := range rows {
		if r["title"] == "archived" && (r["archived"] != true || r["status"] != "stopped") {
			t.Errorf("archived row = %v, want archived:true with its stored status", r)
		}
	}
}

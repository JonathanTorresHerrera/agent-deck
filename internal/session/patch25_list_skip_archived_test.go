// Patch 25: `list --json` does not re-probe archived sessions.
//
// A fresh CLI process has no recheck timestamps, so every probed row costs a
// tmux query, and the substate read reaches the transcript glob. On a 366-row
// deck (most of it archived) `agent-deck list --json` took 49-64 s (measured
// 2026-09-28), stalling agent-deck-mcp and the bus postman.
package session

import "testing"

func TestPatch25_ArchivedRowsAreCachedWhateverTheirStatus(t *testing.T) {
	live := NewInstanceWithTool("live", t.TempDir(), "claude")
	live.Status = StatusRunning
	archived := NewInstanceWithTool("archived", t.TempDir(), "claude")
	archived.Status = StatusError
	archived.ArchivedAt = archived.CreatedAt

	refresh, cached := CLIStatusCandidates([]*Instance{live, archived})
	if !cached[archived] {
		t.Fatalf("archived error row not cached")
	}
	for _, inst := range refresh {
		if inst == archived {
			t.Fatalf("archived row would be probed")
		}
	}
	if cached[live] || len(refresh) != 1 || refresh[0] != live {
		t.Fatalf("live row must still be refreshed: refresh=%d cached[live]=%v", len(refresh), cached[live])
	}
}

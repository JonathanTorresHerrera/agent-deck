// Patch 22: the "/" search finds a session by its Deck ID or its tool's
// session ID, not only by title, path and tool name.
//
// JT searched for a Deck ID copied from the preview (1a2724ac-1790560958) and
// got no result: FilterByQuery matched only Title, ProjectPath and Tool.
// Reported 2026-09-28.
package session

import "testing"

func patch22Fleet() []*Instance {
	return []*Instance{
		{ID: "1a2724ac-1790560958", Title: "Vita HCFA", ProjectPath: "/mnt/d/Dev_Projects/vita-ehr", Tool: "claude",
			ClaudeSessionID: "7e60e581-5c76-4523-ae62-5de8741cd18c", GroupPath: "vita-ehr"},
		{ID: "9ea27510-1790439281", Title: "Agent deck", ProjectPath: "/home/jtorres", Tool: "codex",
			CodexSessionID: "0199aa11-bb22-7c33-8d44-ee5566ff7788", GroupPath: "global"},
		{ID: "55aa66bb-1790000000", Title: "Worktree job", ProjectPath: "/repo", Tool: "gemini",
			GeminiSessionID: "gem-abc-123", WorktreePath: "/repo/.worktrees/feature-zeta", GroupPath: "omni/WorkTrees"},
	}
}

func titles(list []*Instance) []string {
	out := make([]string, 0, len(list))
	for _, i := range list {
		out = append(out, i.Title)
	}
	return out
}

func expectOnly(t *testing.T, query, want string) {
	t.Helper()
	got := FilterByQuery(patch22Fleet(), query)
	if len(got) != 1 || got[0].Title != want {
		t.Errorf("query %q: got %v, want only %q", query, titles(got), want)
	}
}

func TestPatch22_FindsByFullDeckID(t *testing.T) {
	expectOnly(t, "1a2724ac-1790560958", "Vita HCFA")
}

func TestPatch22_FindsByPartialDeckIDCaseInsensitive(t *testing.T) {
	expectOnly(t, "1A2724AC", "Vita HCFA")
}

func TestPatch22_FindsByToolSessionID(t *testing.T) {
	expectOnly(t, "7e60e581-5c76-4523-ae62-5de8741cd18c", "Vita HCFA") // Claude
	expectOnly(t, "0199aa11", "Agent deck")                            // Codex
	expectOnly(t, "gem-abc-123", "Worktree job")                       // Gemini
}

func TestPatch22_FindsByWorktreePathAndGroup(t *testing.T) {
	expectOnly(t, "feature-zeta", "Worktree job")
	expectOnly(t, "omni/worktrees", "Worktree job")
}

func TestPatch22_TrimsPastedWhitespace(t *testing.T) {
	expectOnly(t, "  1a2724ac-1790560958\n", "Vita HCFA")
}

func TestPatch22_NameAndStatusFiltersUnchanged(t *testing.T) {
	expectOnly(t, "agent deck", "Agent deck")
	fleet := patch22Fleet()
	fleet[0].Status = StatusWaiting
	if got := FilterByQuery(fleet, "waiting"); len(got) != 1 || got[0].Title != "Vita HCFA" {
		t.Errorf("status filter changed: got %v", titles(got))
	}
}

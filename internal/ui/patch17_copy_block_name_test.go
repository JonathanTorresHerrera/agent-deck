// Patch 17: the "All info (block)" copy entry (Shift+C then a) carries the
// session name.
//
// The block held Path (or Repo/Path/Branch, or the Paths list) and the
// Session ID, but not the name — so a paste into an issue, a chat or another
// agent's prompt identified the session only by an opaque UUID and a
// directory that many sessions share. Reported 2026-09-26.
//
// The name leads the block: it is what a human reading the paste looks for
// first, and it is the one value that tells two sessions in the same
// directory apart.
package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestBuildSessionInfoForCopy_LeadsWithTheSessionName(t *testing.T) {
	inst := &session.Instance{
		Title:       "WO-VITA-HCFA-SIG-01 HCFA + Sig on File (Aida)",
		ProjectPath: "/tmp/scratch",
	}

	got := buildSessionInfoForCopy(inst)
	want := "Name: WO-VITA-HCFA-SIG-01 HCFA + Sig on File (Aida)"

	if !strings.Contains(got, want) {
		t.Fatalf("block is missing the session name line %q, got:\n%s", want, got)
	}
	if first := strings.SplitN(got, "\n", 2)[0]; first != want {
		t.Errorf("first line = %q, want the name to lead the block", first)
	}
	// Nothing that was there before may go missing.
	if !strings.Contains(got, "Path: /tmp/scratch") {
		t.Errorf("adding the name dropped the path, got:\n%s", got)
	}
}

// The name appears for every session shape, not only plain ones.
func TestBuildSessionInfoForCopy_NameInWorktreeAndMultiRepoShapes(t *testing.T) {
	worktree := &session.Instance{
		Title:            "feature-x work",
		ProjectPath:      "/repo/.worktrees/x",
		WorktreePath:     "/repo/.worktrees/x",
		WorktreeRepoRoot: "/repo",
		WorktreeBranch:   "feature/x",
	}
	if got := buildSessionInfoForCopy(worktree); !strings.HasPrefix(got, "Name: feature-x work\n") {
		t.Errorf("worktree block should lead with the name, got:\n%s", got)
	}

	multi := &session.Instance{
		Title:            "cross-repo sweep",
		ProjectPath:      "/repos/api",
		MultiRepoEnabled: true,
		AdditionalPaths:  []string{"/repos/web"},
	}
	if got := buildSessionInfoForCopy(multi); !strings.HasPrefix(got, "Name: cross-repo sweep\n") {
		t.Errorf("multi-repo block should lead with the name, got:\n%s", got)
	}
}

// A blank title must not produce a dangling "Name:" label — same rule the
// block already applies to an undetected Session ID.
func TestBuildSessionInfoForCopy_BlankNameEmitsNoLabel(t *testing.T) {
	for _, title := range []string{"", "   "} {
		inst := &session.Instance{Title: title, ProjectPath: "/tmp/scratch"}
		if got := buildSessionInfoForCopy(inst); strings.Contains(got, "Name:") {
			t.Errorf("title %q produced a Name line, got:\n%s", title, got)
		}
	}
}

// The picker's "a" entry is built from the same function, so it must carry
// the name too — this is the path JT actually uses.
func TestCopyPicker_AllInfoEntryIncludesTheName(t *testing.T) {
	inst := &session.Instance{Title: "Agent-Deck", ProjectPath: "/home/jtorres"}
	block := findField(t, buildCopyFields(inst), "All info (block)")
	if !strings.Contains(block.value, "Name: Agent-Deck") {
		t.Errorf("'a' entry is missing the session name, got:\n%s", block.value)
	}
}

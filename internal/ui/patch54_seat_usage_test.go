package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func p54Seat(t *testing.T, seat, body string) {
	t.Helper()
	dir := t.TempDir()
	prev := seatUsageDir
	seatUsageDir = func() string { return dir }
	t.Cleanup(func() { seatUsageDir = prev })
	if err := os.WriteFile(filepath.Join(dir, seat+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPatch54_PreviewShowsAccountUsageAndChecked(t *testing.T) {
	now := time.Date(2026, 11, 9, 20, 30, 0, 0, time.UTC)
	seatUsageNow = func() time.Time { return now }
	t.Cleanup(func() { seatUsageNow = time.Now })
	p54Seat(t, "claude-c0e6cb2b", `{"seat":"claude-c0e6cb2b","provider":"claude",
	 "email":"aida@devcodesgpt.com","plan":"Ultra",
	 "windows":[{"name":"five_hour","label":"5h","used_pct":19,"resets_at":"2026-11-09T22:00:00Z"},
	            {"name":"seven_day","label":"week","used_pct":85,"resets_at":"2026-11-13T09:00:00Z"}],
	 "usage_observed_at":"2026-11-09T20:18:00Z","checked_at":"2026-11-09T20:27:00Z"}`)

	inst := &session.Instance{Tool: "claude", Account: "claude-c0e6cb2b"}
	var b strings.Builder
	renderSeatUsageLines(&b, inst, 120)
	out := ansi.Strip(b.String())
	for _, want := range []string{
		"Account: aida@devcodesgpt.com · Ultra",
		"5h 81% left (resets ", "week 15% left (resets ",
		"Checked: usage as of ", "(12m ago)", "refreshed ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	for i, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if cellWidth(l) > 120 {
			t.Fatalf("line %d too wide: %q", i, l)
		}
	}
}

func TestPatch54_NoFileNoLines(t *testing.T) {
	p54Seat(t, "other", `{}`)
	var b strings.Builder
	renderSeatUsageLines(&b, &session.Instance{Tool: "codex", Account: "codex-9"}, 100)
	if b.Len() != 0 {
		t.Fatalf("no usage file must render nothing, got %q", b.String())
	}
}

func TestPatch54_CodexNoteAndPlanWords(t *testing.T) {
	p54Seat(t, "codex-3", `{"seat":"codex-3","provider":"codex","email":"c@x.com",
	 "plan":"self_serve_business_prolite","windows":[],"note":"workspace owner credits depleted"}`)
	var b strings.Builder
	renderSeatUsageLines(&b, &session.Instance{Tool: "codex", Account: "codex-3"}, 120)
	out := ansi.Strip(b.String())
	if !strings.Contains(out, "self serve business prolite") || !strings.Contains(out, "workspace owner credits depleted") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestPatch54_RejectsPathSeat(t *testing.T) {
	if loadSeatUsage("../x") != nil || loadSeatUsage("a/b") != nil {
		t.Fatal("seat names with path separators must not be read")
	}
}

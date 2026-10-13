package ui

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch49_PreviewSeatSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, c := range []struct {
		inst *session.Instance
		want string
	}{
		{&session.Instance{Tool: "claude", Account: "claude-3"}, " @claude-3"},
		{&session.Instance{Tool: "codex", Account: "codex-2"}, " @codex-2"},
		{&session.Instance{Tool: "claude"}, " @claude"},
		{&session.Instance{Tool: "codex"}, " @codex"},
		{&session.Instance{Tool: "shell"}, ""},
		{nil, ""},
	} {
		if got := previewSeatSuffix(c.inst); got != c.want {
			t.Fatalf("previewSeatSuffix(%+v) = %q, want %q", c.inst, got, c.want)
		}
	}
}

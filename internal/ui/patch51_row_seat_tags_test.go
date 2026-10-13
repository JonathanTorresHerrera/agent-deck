package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch51_RowSeatTagModes(t *testing.T) {
	forceTrueColorProfile()
	named := &session.Instance{ID: "n", Title: "WO-NAMED", Account: "claude-2", Tool: "claude"}
	namedState := sessionRenderState{status: session.StatusIdle, tool: "claude", title: named.Title,
		account: "claude-2", accountDisplay: newAccountPresentation("claude-2", true)}
	unset := &session.Instance{ID: "u", Title: "WO-UNSET", Tool: "claude"}
	unsetState := sessionRenderState{status: session.StatusIdle, tool: "claude", title: unset.Title,
		defaultSeat: "claude", accountDisplay: seatPresentation("", "claude", true)}

	for _, c := range []struct {
		mode             string
		named, unsetSeen bool
	}{
		{session.RowSeatTagsAll, true, true},
		{session.RowSeatTagsNamed, true, false},
		{session.RowSeatTagsOff, false, false},
	} {
		for _, wrap := range []bool{false, true} {
			h := &Home{width: 140, rowSeatTags: c.mode, wrapRows: wrap}
			if got := strings.Contains(ansi.Strip(p31RenderRow(h, named, namedState, 100)), "@claude-2"); got != c.named {
				t.Fatalf("mode %s wrap %v: named tag shown=%v", c.mode, wrap, got)
			}
			if got := strings.Contains(ansi.Strip(p31RenderRow(h, unset, unsetState, 100)), "@claude"); got != c.unsetSeen {
				t.Fatalf("mode %s wrap %v: default tag shown=%v", c.mode, wrap, got)
			}
		}
	}
}

func TestPatch51_ConfigFallback(t *testing.T) {
	if got := (session.DisplaySettings{}).GetRowSeatTags(); got != session.RowSeatTagsNamed {
		t.Fatalf("unset = %q, want named", got)
	}
	if got := (session.DisplaySettings{ShowInheritedAccount: true}).GetRowSeatTags(); got != session.RowSeatTagsAll {
		t.Fatalf("legacy true = %q, want all", got)
	}
	if got := (session.DisplaySettings{RowSeatTags: "off", ShowInheritedAccount: true}).GetRowSeatTags(); got != session.RowSeatTagsOff {
		t.Fatalf("explicit off = %q", got)
	}
}

func TestPatch51_SettingCyclesAndSaves(t *testing.T) {
	s := &SettingsPanel{cursor: int(SettingShowInheritedAccount)} // Named
	for _, want := range []string{session.RowSeatTagsAll, session.RowSeatTagsOff, session.RowSeatTagsNamed} {
		s.toggleValue()
		if got := rowSeatTagValues[s.rowSeatTags]; got != want {
			t.Fatalf("cycle: got %s, want %s", got, want)
		}
		if s.showInheritedAccount != (want == session.RowSeatTagsAll) {
			t.Fatalf("legacy flag out of sync at %s", want)
		}
	}
}

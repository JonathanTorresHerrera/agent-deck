package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func p39Home(t *testing.T, states map[string]sessionRenderState) *Home {
	t.Helper()
	h := &Home{width: 200, height: 40}
	h.sessionRenderSnapshot.Store(states)
	return h
}

func p39Fixture() map[string]sessionRenderState {
	return map[string]sessionRenderState{
		"c1": {status: session.StatusRunning, tool: "claude"},
		"c2": {status: session.StatusWaiting, tool: "claude"},
		"c3": {status: session.StatusIdle, tool: "claude"},
		"c4": {status: session.StatusStopped, tool: "claude"},                 // not live
		"c5": {status: session.StatusError, tool: "claude"},                   // not live
		"c6": {status: session.StatusWaiting, tool: "claude", archived: true}, // other partition
		"x1": {status: session.StatusWaiting, tool: "codex"},
		"x2": {status: session.StatusIdle, tool: "codex"},
		"g1": {status: session.StatusIdle, tool: "grok"},
		"s1": {status: session.StatusIdle, tool: ""}, // counts as shell
	}
}

func TestPatch39_ProviderCountsLiveActivePartition(t *testing.T) {
	h := p39Home(t, p39Fixture())
	got := h.providerCounts()
	want := []providerCount{
		{tool: "claude", live: 3, running: 1},
		{tool: "codex", live: 2, running: 0},
		{tool: "grok", live: 1, running: 0},
		{tool: "shell", live: 1, running: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("providerCounts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("providerCounts[%d] = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestPatch39_RenderLevels(t *testing.T) {
	counts := []providerCount{{tool: "claude", live: 65, running: 7}, {tool: "codex", live: 2}}
	full := stripAnsi(renderProviderCounts(counts, 0))
	for _, want := range []string{"C Claude 65", "(7▶)", "G ChatGPT 2"} {
		if !strings.Contains(full, want) {
			t.Fatalf("level 0 %q missing %q", full, want)
		}
	}
	if strings.Contains(full, "ChatGPT 2 (") {
		t.Fatalf("a provider with nothing running must not show a running figure: %q", full)
	}
	short := stripAnsi(renderProviderCounts(counts, 1))
	if strings.Contains(short, "▶") || !strings.Contains(short, "Claude 65") {
		t.Fatalf("level 1 must drop only the running figure: %q", short)
	}
	if renderProviderCounts(counts, 2) != "" || renderProviderCounts(nil, 0) != "" {
		t.Fatal("level 2 and no counts must render nothing")
	}
}

func TestPatch39_FilterBarShowsProvidersAndFits(t *testing.T) {
	for _, w := range []int{200, 150, 120, 100, 80, 60} {
		h := p39Home(t, p39Fixture())
		h.width = w
		bar := h.renderFilterBar()
		plain := stripAnsi(bar)
		if lipgloss.Width(bar) > w {
			t.Fatalf("width %d: filter bar is %d wide", w, lipgloss.Width(bar))
		}
		if w >= 150 && !strings.Contains(plain, "C Claude 3 (1▶) · G ChatGPT 2 · X Grok 1 · $ shell 1") {
			t.Fatalf("width %d: provider counts missing: %q", w, plain)
		}
	}
}

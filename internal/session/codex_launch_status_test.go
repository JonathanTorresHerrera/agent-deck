package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/send"
)

// WO-DECK-CODEX-LAUNCH-01: a fresh Codex v0.161 session sits at its splash
// with an idle composer, no hook file (notify fires only at turn end, and no
// turn ever ran), and a braille logo whose row "⠙⠛⠛⠛⠻…" read as a spinner.
// Every status reader (`session show`, `list --json`, `session send`'s
// readiness and --defer-if-busy gate, the MCP's busy check) reported running
// and the first message was refused for good.

// codexLaunchFrame loads a captured pane from the tmux fixtures, collapsing
// blank runs and pulling the centred logo left so the whole frame fits the
// test pane without wrapping.
func codexLaunchFrame(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "tmux", "testdata", "codex_launch", name))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	blank := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		for n := 0; n < 60 && strings.HasPrefix(line, " "); n++ {
			line = line[1:]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestCodexSplash_FreshProcessReportsWaiting(t *testing.T) {
	inst, cleanup := startCodexPaneInstance(t, "splash", codexLaunchFrame(t, "splash-idle-short.txt"))
	defer cleanup()
	pane, err := inst.tmuxSession.CapturePaneFresh()
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.Contains(pane, "⠙⠛⠛⠛⠻") {
		t.Fatalf("the logo row that triggered the bug is not on screen:\n%s", pane)
	}
	storage, err := NewStorageWithProfile("_test-codex-splash")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer storage.Close()

	// The row as the daemon left it, read by a fresh process: what `session
	// show --json` and `session send`'s fetchHookDrivenStatus both do.
	for pass := 1; pass <= 2; pass++ {
		fresh := persistAndReload(t, storage, inst, StatusRunning)
		status, sub := cliPass(t, fresh)
		if status != StatusWaiting {
			t.Fatalf("fresh process %d = %q (substate %q), want waiting: the splash shows an idle composer", pass, status, sub)
		}
		if send.StatusIsBusy(string(status)) {
			t.Fatalf("send gate would refuse: %q counts as busy", status)
		}
	}
}

func TestCodexModelRejection_StatusErrorWithModel(t *testing.T) {
	inst, cleanup := startCodexPaneInstance(t, "reject", codexLaunchFrame(t, "model-400-inferred.txt"))
	defer cleanup()

	status, sub := cliPass(t, inst)
	if status != StatusError {
		t.Fatalf("status = %q, want error for a rejected turn", status)
	}
	if sub != SubstateModelUnsupported {
		t.Fatalf("substate = %q, want %q", sub, SubstateModelUnsupported)
	}
	if got := inst.SubstateDetail(); !strings.HasPrefix(got, "model gpt-5.5-codex: ") {
		t.Fatalf("substate detail = %q, want the rejected model named", got)
	}
}

const (
	rolloutTaskStarted  = `{"timestamp":"2026-10-13T18:15:58.367Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`
	rolloutUserMessage  = `{"timestamp":"2026-10-13T18:15:59.396Z","type":"response_item","payload":{"type":"message","role":"user","content":[]}}`
	rolloutRejected     = `{"timestamp":"2026-10-13T18:16:00.361Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"error":{"message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'gpt-5.5-codex' model is not supported when using Codex with a ChatGPT account.\"}}"}}}`
	rolloutTaskStarted2 = `{"timestamp":"2026-10-13T18:22:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t2"}}`
	rolloutCompletedOK  = `{"timestamp":"2026-10-13T18:23:00.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t2","last_agent_message":"done"}}`
)

func TestCodexLastTurnErrorFromLines(t *testing.T) {
	const want = "model gpt-5.5-codex: The 'gpt-5.5-codex' model is not supported when using Codex with a ChatGPT account."
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"rejected first turn", []string{rolloutTaskStarted, rolloutUserMessage, rolloutRejected}, want},
		{"newer turn started after the rejection", []string{rolloutTaskStarted, rolloutRejected, rolloutTaskStarted2}, ""},
		{"newer turn completed after the rejection", []string{rolloutTaskStarted, rolloutRejected, rolloutTaskStarted2, rolloutCompletedOK}, ""},
		{"turn still running", []string{rolloutTaskStarted, rolloutUserMessage}, ""},
		{"no turns", nil, ""},
		{"garbage lines are skipped", []string{rolloutTaskStarted, rolloutRejected, "{not json"}, want},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexLastTurnErrorFromLines(tc.lines); got != tc.want {
				t.Errorf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestCodexLastTurnErrorIgnoresOtherTools(t *testing.T) {
	inst := &Instance{ID: "c1", Tool: "claude"}
	if got := inst.CodexLastTurnError(); got != "" {
		t.Fatalf("claude session reported a codex turn error %q", got)
	}
}

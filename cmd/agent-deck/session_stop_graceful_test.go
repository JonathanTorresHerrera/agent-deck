package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeExitTarget struct {
	pane    string
	capErr  error
	pid     int
	pidErr  error
	typeErr error
	typed   []string
}

func (f *fakeExitTarget) CapturePaneFresh() (string, error) { return f.pane, f.capErr }
func (f *fakeExitTarget) PanePID() (int, error)             { return f.pid, f.pidErr }
func (f *fakeExitTarget) TypeCommand(c string) error {
	f.typed = append(f.typed, c)
	return f.typeErr
}

// fakeClock advances only when the loop sleeps; the process dies after
// dieAfter polls (never when dieAfter < 0).
func fakeClock(dieAfter int) (gracefulClock, *int) {
	now := time.Unix(1_000_000, 0)
	polls := 0
	return gracefulClock{
		now:   func() time.Time { return now },
		sleep: func(d time.Duration) { now = now.Add(d) },
		alive: func(int) bool {
			polls++
			return dieAfter < 0 || polls <= dieAfter
		},
	}, &polls
}

const emptyClaudeComposer = "────────────────\n❯ \n────────────────\n"

func TestRunGracefulExit_TypesExitAndWaitsForProcess(t *testing.T) {
	target := &fakeExitTarget{pane: emptyClaudeComposer, pid: 4242}
	clk, _ := fakeClock(3)
	res := runGracefulExit(target, "claude", 30*time.Second, clk)
	if !res.Attempted || !res.Exited || res.FellBack {
		t.Fatalf("want attempted+exited, no fallback; got %+v", res)
	}
	if len(target.typed) != 1 || target.typed[0] != "/exit" {
		t.Fatalf("typed = %v, want [/exit]", target.typed)
	}
	if res.WaitedMs != 3*gracefulPollInterval.Milliseconds() {
		t.Fatalf("waited_ms = %d", res.WaitedMs)
	}
}

func TestRunGracefulExit_PerAgentCommands(t *testing.T) {
	for tool, want := range map[string]string{"claude": "/exit", "codex": "/exit", "gemini": "/quit"} {
		target := &fakeExitTarget{pid: 1}
		clk, _ := fakeClock(0)
		res := runGracefulExit(target, tool, time.Second, clk)
		if res.Command != want || len(target.typed) != 1 || target.typed[0] != want {
			t.Fatalf("%s: command %q typed %v, want %q", tool, res.Command, target.typed, want)
		}
	}
}

func TestRunGracefulExit_TimeoutFallsBack(t *testing.T) {
	target := &fakeExitTarget{pane: emptyClaudeComposer, pid: 7}
	clk, _ := fakeClock(-1)
	res := runGracefulExit(target, "claude", 2*time.Second, clk)
	if !res.Attempted || res.Exited || !res.FellBack {
		t.Fatalf("want attempted, not exited, fell back; got %+v", res)
	}
	if res.WaitedMs < 2000 || res.WaitedMs > 2000+gracefulPollInterval.Milliseconds() {
		t.Fatalf("waited_ms = %d, want ~2000", res.WaitedMs)
	}
	if !strings.Contains(res.Reason, "did not exit") {
		t.Fatalf("reason = %q", res.Reason)
	}
}

func TestRunGracefulExit_NeverTypesIntoADraft(t *testing.T) {
	target := &fakeExitTarget{pane: "────────────────\n❯ half-typed prompt from JT\n────────────────\n", pid: 7}
	clk, polls := fakeClock(0)
	res := runGracefulExit(target, "claude", time.Second, clk)
	if res.Attempted || !res.FellBack || len(target.typed) != 0 || *polls != 0 {
		t.Fatalf("draft must block typing; got %+v typed=%v", res, target.typed)
	}
	if !strings.Contains(res.Reason, "draft") {
		t.Fatalf("reason = %q", res.Reason)
	}
}

func TestRunGracefulExit_UnreadablePaneFallsBack(t *testing.T) {
	target := &fakeExitTarget{capErr: errors.New("capture timed out"), pid: 7}
	clk, _ := fakeClock(0)
	res := runGracefulExit(target, "claude", time.Second, clk)
	if res.Attempted || !res.FellBack || len(target.typed) != 0 {
		t.Fatalf("unreadable composer must not be typed into; got %+v", res)
	}
}

func TestRunGracefulExit_UnknownAgentFallsBack(t *testing.T) {
	target := &fakeExitTarget{pid: 7}
	clk, _ := fakeClock(0)
	res := runGracefulExit(target, "opencode", time.Second, clk)
	if res.Attempted || !res.FellBack || len(target.typed) != 0 {
		t.Fatalf("unknown agent must fall back untouched; got %+v", res)
	}
}

func TestRunGracefulExit_PIDOrTypeFailureFallsBack(t *testing.T) {
	clk, _ := fakeClock(0)
	if res := runGracefulExit(&fakeExitTarget{pidErr: errors.New("no pane")}, "claude", time.Second, clk); res.Attempted || !res.FellBack {
		t.Fatalf("pid failure: %+v", res)
	}
	if res := runGracefulExit(&fakeExitTarget{pid: 1, typeErr: errors.New("send-keys failed")}, "claude", time.Second, clk); res.Attempted || !res.FellBack {
		t.Fatalf("type failure: %+v", res)
	}
}

func TestValidateGracefulTimeout(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second, maxGracefulTimeout + time.Second} {
		if validateGracefulTimeout(d) == nil {
			t.Fatalf("%s should be rejected", d)
		}
	}
	if err := validateGracefulTimeout(defaultGracefulTimeout); err != nil {
		t.Fatal(err)
	}
}

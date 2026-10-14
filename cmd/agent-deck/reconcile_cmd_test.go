package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestClassifyReconcileFindsOrphansAndMissingEnd(t *testing.T) {
	dead := session.NewInstance("dead", t.TempDir())
	dead.ID = "dead-id"
	dead.Status = session.StatusRunning
	archived := session.NewInstance("archived", t.TempDir())
	archived.ID = "archived-id"
	archived.ArchivedAt = time.Now()
	archived.Status = session.StatusWaiting
	rows := map[string][]*session.Instance{"default": {dead, archived}}
	live := map[string][]string{"": {archived.GetTmuxSession().Name, "agentdeck_orphan_1234"}}
	plan := classifyReconcile(rows, live, map[string]reconcileHook{"dead-id": {Status: "running", Event: "UserPromptSubmit"}})
	if plan.counts.Total != 2 || plan.counts.Archived != 1 || plan.counts.ArchivedTmuxAlive != 1 || plan.counts.KilledWithoutSessionEnd != 1 || plan.counts.StaleHooks != 1 || plan.counts.OrphanTmux != 1 {
		t.Fatalf("wrong counts: %+v", plan.counts)
	}
	if plan.orchans[0].name != "agentdeck_orphan_1234" {
		t.Fatalf("wrong orphan: %+v", plan.orchans)
	}
}

func TestReconcileHookClaimIsIdempotent(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "xdg"))
	first, err := claimReconcileHook("12345678-12345678", time.Time{})
	if err != nil || !first {
		t.Fatalf("first claim=%t err=%v", first, err)
	}
	second, err := claimReconcileHook("12345678-12345678", time.Time{})
	if err != nil || second {
		t.Fatalf("second claim=%t err=%v", second, err)
	}
	if _, err := os.Stat(filepath.Join(session.GetHooksDir(), "reconciled", "12345678-12345678.claimed")); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyReconcileTerminalRecordIsIdempotent(t *testing.T) {
	inst := session.NewInstance("ended", t.TempDir())
	inst.ID = "ended-id"
	inst.Status = session.StatusStopped
	inst.LastStartedAt = time.Now().Add(-time.Minute)
	rows := map[string][]*session.Instance{"default": {inst}}
	plan := classifyReconcile(rows, map[string][]string{"": {}}, map[string]reconcileHook{
		inst.ID: {Status: "dead", Event: "SessionEnd", Timestamp: time.Now().Unix()},
	})
	if plan.counts.KilledWithoutSessionEnd != 0 || plan.counts.StaleHooks != 0 || len(plan.candidates) != 0 {
		t.Fatalf("terminal hook was scheduled again: %+v", plan)
	}
}

func TestReplayMissedEndHooksRunsOnce(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "xdg"))
	config := session.GetGeminiConfigDir()
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(base, "receipt")
	settings := `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"printf x >> ` + receipt + `"}]}]}}`
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	inst := session.NewInstance("replay-once", base)
	inst.ID = "replay-once-id"
	inst.Tool = "gemini"
	for range 2 {
		if err := replayMissedEndHooks(inst, reconcileHook{SessionID: "native-id"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(receipt)
	if err != nil || string(got) != "x" {
		t.Fatalf("receipt=%q err=%v", got, err)
	}
}

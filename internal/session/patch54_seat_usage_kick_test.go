package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPatch54_KickRunsRefresherWithBothSeats(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	script := filepath.Join(dir, "seat-usage-refresh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+out+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := seatUsageRefreshPath
	seatUsageRefreshPath = func() string { return script }
	t.Cleanup(func() { seatUsageRefreshPath = prev })
	t.Setenv("AGENTDECK_SEAT_USAGE_REFRESH", "")

	if !kickSeatUsageRefresh("claude-2", "claude-3") {
		t.Fatal("refresher not started")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil {
			if got := strings.TrimSpace(string(b)); got != "--seat claude-2 --seat claude-3" {
				t.Fatalf("args = %q", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("refresher did not run")
}

func TestPatch54_KickIsNoOpWithoutScriptOrWhenOff(t *testing.T) {
	prev := seatUsageRefreshPath
	seatUsageRefreshPath = func() string { return filepath.Join(t.TempDir(), "missing") }
	t.Cleanup(func() { seatUsageRefreshPath = prev })
	if kickSeatUsageRefresh("x") {
		t.Fatal("missing script must be a no-op")
	}
	t.Setenv("AGENTDECK_SEAT_USAGE_REFRESH", "off")
	if kickSeatUsageRefresh("x") {
		t.Fatal("off must be a no-op")
	}
}

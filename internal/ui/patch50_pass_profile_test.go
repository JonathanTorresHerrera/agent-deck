package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch50_SlowPassIsProfiled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	dir, err := session.HealthLogDir("")
	if err != nil {
		t.Skipf("no health dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	slowPassLog.Lock()
	slowPassLog.lastSlow, slowPassLog.lastFast = time.Time{}, time.Time{}
	slowPassLog.Unlock()

	p := newPassProfile(time.Now(), 0)
	p.mark("claims")
	p.Slowest = []string{"a=60ms", "b=900ms", "c=120ms"}
	p.finish("", 5*time.Second, 10, 42)

	data, err := os.ReadFile(filepath.Join(dir, "slow-passes.jsonl"))
	if err != nil {
		t.Fatalf("slow pass not written: %v", err)
	}
	var got passProfile
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Over || got.TmuxTotal != 42 || len(got.Stages) != 2 || got.Stages[0].Name != "claims" {
		t.Fatalf("unexpected profile: %+v", got)
	}
	if got.Slowest[0] != "b=900ms" || got.Slowest[2] != "a=60ms" {
		t.Fatalf("slowest must sort by duration: %v", got.Slowest)
	}

	// A second slow pass inside the rate limit is not written.
	newPassProfile(time.Now(), 0).finish("", 5*time.Second, 10, 1)
	data, _ = os.ReadFile(filepath.Join(dir, "slow-passes.jsonl"))
	if n := strings.Count(string(data), "\n"); n != 1 {
		t.Fatalf("rate limit: want 1 line, got %d", n)
	}
}

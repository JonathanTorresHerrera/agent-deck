package health

import (
	"strings"
	"testing"
	"time"
)

// A burst of slow passes shorter than the sustain window never warns; the
// same slowness held past it does, and one fast pass restarts the clock.
func TestPatch46_BurstDoesNotWarnSustainedDoes(t *testing.T) {
	p40Clear(t)
	t.Cleanup(SetStatusPassSustainFor(30 * time.Second))
	now := time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC)
	prevNow := statusNow
	statusNow = func() time.Time { return now }
	t.Cleanup(func() { statusNow = prevNow })

	// The 2026-10-05 11:00 burst: a few seconds of 1190 ms passes.
	for i := 0; i < 5; i++ {
		if w := BudgetWarning(1190*time.Millisecond, 457, 177); w != "" {
			t.Fatalf("burst pass %d warned: %q", i, w)
		}
		now = now.Add(2 * time.Second)
	}
	// One fast pass ends the run.
	BudgetWarning(40*time.Millisecond, 457, 10)
	now = now.Add(2 * time.Second)

	// Sustained slowness: warns once 30 s have passed since the run began.
	var w string
	for i := 0; i < 16; i++ {
		w = BudgetWarning(1190*time.Millisecond, 457, 177)
		if i < 15 && w != "" {
			t.Fatalf("warned after only %d s", i*2)
		}
		now = now.Add(2 * time.Second)
	}
	if !strings.Contains(w, "exceeds 607 ms budget") {
		t.Fatalf("sustained 30 s breach = %q, want the warning", w)
	}
}

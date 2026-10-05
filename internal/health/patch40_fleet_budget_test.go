package health

import (
	"strings"
	"testing"
	"time"
)

func TestPatch40_BudgetScalesWithFleet(t *testing.T) {
	cases := map[int]time.Duration{
		0:   250 * time.Millisecond,
		100: 250 * time.Millisecond,
		150: 300 * time.Millisecond,
		445: 595 * time.Millisecond,
	}
	for n, want := range cases {
		if got := StatusPassBudgetFor(n); got != want {
			t.Errorf("StatusPassBudgetFor(%d) = %v, want %v", n, got, want)
		}
	}
}

func p40Clear(t *testing.T) {
	t.Helper()
	ResetStatusPassBreachState()
	t.Cleanup(SetStatusPassSustainFor(0)) // patch 46's 30 s window has its own test
	statusPassWarmupUntil.Lock()
	statusPassWarmupUntil.t = time.Time{}
	statusPassWarmupUntil.Unlock()
	t.Cleanup(func() {
		ResetStatusPassBreachState()
		statusPassWarmupUntil.Lock()
		statusPassWarmupUntil.t = time.Time{}
		statusPassWarmupUntil.Unlock()
	})
}

func TestPatch40_LargeFleetNormalPassesDoNotWarn(t *testing.T) {
	p40Clear(t)
	for i := 0; i < 6; i++ { // 300 ms passes on 445 sessions: under the 595 ms budget
		if w := BudgetWarning(300*time.Millisecond, 445, 10); w != "" {
			t.Fatalf("pass %d: unexpected warning %q", i, w)
		}
	}
	var w string
	for i := 0; i < 3; i++ { // sustained real slowness still warns, naming the scaled budget
		w = BudgetWarning(900*time.Millisecond, 445, 10)
	}
	if !strings.Contains(w, "exceeds 595 ms budget") {
		t.Fatalf("sustained breach on a large fleet = %q, want the 595 ms budget named", w)
	}
}

func TestPatch40_WarmupSuppressesColdLoad(t *testing.T) {
	p40Clear(t)
	statusPassWarmupUntil.Lock()
	statusPassWarmupUntil.t = time.Now().Add(time.Minute)
	statusPassWarmupUntil.Unlock()
	for i := 0; i < 5; i++ {
		if w := BudgetWarning(1700*time.Millisecond, 50, 1); w != "" {
			t.Fatalf("cold-load pass %d warned: %q", i, w)
		}
	}
}

func TestPatch40_SmallFleetKeepsUpstreamText(t *testing.T) {
	p40Clear(t)
	var w string
	for i := 0; i < 3; i++ {
		w = BudgetWarning(300*time.Millisecond, 100, 1)
	}
	if w != "Health: status pass exceeds 250 ms budget" {
		t.Fatalf("small fleet text changed: %q", w)
	}
}

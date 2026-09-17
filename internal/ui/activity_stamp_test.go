// Patch 12 (A): the preview pane used to render activity as a bare relative
// string ("3h 20m ago"), and replaced it outright with "active now" for
// running sessions — so the one surface the operator reads before entering a
// session never showed an actual clock time, and running sessions showed no
// time at all. On a 100+ session deck that is the difference between
// "roughly recently" and "Tuesday afternoon".
//
// formatActivityStamp is the shared formatter for those lines. It matches the
// format patch 2 established in the analytics panel: relative, then the local
// wall clock in parentheses. Local matters — transcript and hook timestamps
// are UTC, and rendering them raw turned 22:46 Denver into "04:46" (the bug
// patch 2 fixed on the Started: line).
package ui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatActivityStamp_AppendsLocalWallClock(t *testing.T) {
	at := time.Now().Add(-3*time.Hour - 20*time.Minute)
	got := formatActivityStamp(at, false)

	if !strings.Contains(got, "ago") {
		t.Errorf("formatActivityStamp(%v) = %q, want a relative component", at, got)
	}
	want := at.Local().Format("Jan 2 15:04")
	if !strings.Contains(got, "("+want+")") {
		t.Errorf("formatActivityStamp = %q, want it to contain (%s)", got, want)
	}
}

// A running session still gets its clock time. Replacing the whole string
// with "active now" is what hid the timestamp on every running session.
func TestFormatActivityStamp_ActiveNowKeepsTheClock(t *testing.T) {
	at := time.Now().Add(-90 * time.Second)
	got := formatActivityStamp(at, true)

	if !strings.HasPrefix(got, "active now") {
		t.Errorf("formatActivityStamp(running) = %q, want it to lead with 'active now'", got)
	}
	want := at.Local().Format("Jan 2 15:04")
	if !strings.Contains(got, "("+want+")") {
		t.Errorf("formatActivityStamp(running) = %q, want it to still carry (%s)", got, want)
	}
}

// A zero time is unknown, not epoch. Rendering "(Jan 1 00:00)" for a legacy
// row would be worse than saying nothing.
func TestFormatActivityStamp_ZeroIsUnknownWithNoClock(t *testing.T) {
	got := formatActivityStamp(time.Time{}, false)

	if got != "unknown" {
		t.Errorf("formatActivityStamp(zero) = %q, want %q", got, "unknown")
	}
	if strings.Contains(got, "(") {
		t.Errorf("formatActivityStamp(zero) = %q, want no clock component", got)
	}
}

// The local conversion is the point of the parenthesised half: a UTC input
// must not render its UTC hour when the machine is not on UTC.
func TestFormatActivityStamp_RendersLocalNotUTC(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 2026-09-17 04:46 UTC is 2026-09-16 22:46 in Denver.
	utc := time.Date(2026, 9, 17, 4, 46, 0, 0, time.UTC)
	got := formatActivityStamp(utc, false)

	wantLocal := utc.In(denver).Format("Jan 2 15:04")
	if time.Local.String() == denver.String() && !strings.Contains(got, wantLocal) {
		t.Errorf("formatActivityStamp(%v) = %q, want local %s", utc, got, wantLocal)
	}
	// Regardless of the host zone, the rendered clock must equal the input
	// converted to the host's own local zone.
	if !strings.Contains(got, utc.Local().Format("Jan 2 15:04")) {
		t.Errorf("formatActivityStamp = %q, want the input rendered in time.Local", got)
	}
}

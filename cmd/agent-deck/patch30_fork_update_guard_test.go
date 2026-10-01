package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPatch30_ForkUpdateBlocked(t *testing.T) {
	cases := []struct {
		name                                 string
		check, timerStatus, uninstall, block bool
	}{
		{"plain update installs", false, false, false, true},
		{"--check is read-only", true, false, false, false},
		{"--timer-status is read-only", false, true, false, false},
		{"--uninstall-timer removes auto-update", false, false, true, false},
	}
	for _, c := range cases {
		if got := forkUpdateBlocked(c.check, c.timerStatus, c.uninstall); got != c.block {
			t.Errorf("%s: forkUpdateBlocked = %v, want %v", c.name, got, c.block)
		}
	}
}

func TestPatch30_RefusalCarriesMarker(t *testing.T) {
	var b bytes.Buffer
	printForkUpdateRefusal(&b)
	if !strings.Contains(b.String(), forkBuildMarker) || !strings.Contains(b.String(), "merge session") {
		t.Fatalf("refusal text missing marker or merge-session pointer: %q", b.String())
	}
}

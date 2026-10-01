package main

import (
	"fmt"
	"io"
)

// Patch 30 (fork update guard): this binary is a custom fork carrying local
// patches on top of an upstream release. Installing an upstream build in place
// silently drops every patch, so the self-updater refuses to install here.
// New upstream versions are merged by a dedicated session (rebase the patch
// series onto the new tag, run the gate, then install via
// ~/.agent-deck/bin/agent-deck-update.sh after the operator says GO).
//
// forkBuildMarker is also what ~/.agent-deck/bin/adk-binary-guard.sh greps for
// to tell a fork build from a stock one, so keep the string stable.
const forkBuildMarker = "ADK-FORK-BUILD"

const forkUpdateRefusal = forkBuildMarker + ": this agent-deck is a custom fork; installing an upstream release would drop its patches.\n" +
	"New versions are merged by a merge session, not installed in place.\n" +
	"  - see what is available:   agent-deck update --check\n" +
	"  - merge + install:          ~/.agent-deck/bin/agent-deck-update.sh (after the merge session's GO)"

// forkUpdateBlocked reports whether a parsed `agent-deck update` invocation
// would install or schedule an install. Read-only modes stay allowed: --check
// (with or without --json), --timer-status, and --uninstall-timer (removing
// the upstream auto-update timer is exactly what we want).
func forkUpdateBlocked(checkOnly, timerStatus, uninstallTimer bool) bool {
	return !checkOnly && !timerStatus && !uninstallTimer
}

func printForkUpdateRefusal(w io.Writer) {
	fmt.Fprintln(w, forkUpdateRefusal)
}

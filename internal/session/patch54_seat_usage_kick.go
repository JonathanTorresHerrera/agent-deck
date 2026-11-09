package session

// Patch 54: after a successful seat switch, refresh the usage files of both
// seats so the PREVIEW pane's Account/Usage lines follow the switch at once
// instead of at the next hourly run. Every switch path (the TUI's Shift+P,
// `agent-deck session switch-account`, Jev's rotator) ends in
// SwitchAttempt.Finish, which calls this. Fire-and-forget: the refresher is a
// local script, and a missing script (tests, other hosts) is a no-op.
// AGENTDECK_SEAT_USAGE_REFRESH=off disables it.

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/asheshgoplani/agent-deck/internal/agentpaths"
)

var seatUsageRefreshPath = func() string {
	dir, err := agentpaths.LegacyDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bin", "seat-usage-refresh")
}

// kickSeatUsageRefresh starts the refresher for the given seats ("" = the
// default seat, which refreshes every seat). It reports whether it started.
func kickSeatUsageRefresh(seats ...string) bool {
	if os.Getenv("AGENTDECK_SEAT_USAGE_REFRESH") == "off" {
		return false
	}
	path := seatUsageRefreshPath()
	if st, err := os.Stat(path); path == "" || err != nil || st.IsDir() {
		return false
	}
	args := []string{}
	for _, s := range seats {
		if s == "" {
			args = nil // the default seat has no name to target: refresh all
			break
		}
		args = append(args, "--seat", s)
	}
	cmd := exec.Command(path, args...) // #nosec G204 -- fixed local script; args are seat names
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { _ = cmd.Wait() }()
	return true
}

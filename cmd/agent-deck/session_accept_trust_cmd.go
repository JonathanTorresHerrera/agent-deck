package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// handleSessionAcceptTrust answers a Claude/Codex folder-trust dialog on the
// session's pane, only inside session.TrustAutoAcceptRoots
// (WO-DECK-CODEX-LAUNCH-01). Launchers call it when readiness finds the pane
// held by a trust dialog. Exit 0 for accepted / not_present, 1 otherwise.
func handleSessionAcceptTrust(profile string, args []string) {
	fs := flag.NewFlagSet("session accept-trust", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session accept-trust <id|title> [--json]")
		fmt.Println()
		fmt.Println("Accept a folder-trust dialog on the session's pane, but only for folders inside")
		fmt.Printf("%s. Outcomes: accepted, not_present, refused_outside_roots, failed.\n", strings.Join(session.TrustAutoAcceptRoots, ", "))
		fmt.Println()
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}
	out := NewCLIOutput(*jsonOutput, false)
	if fs.Arg(0) == "" {
		out.Error("session id or title is required", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		os.Exit(1)
	}
	inst, errMsg, errCode := ResolveSession(fs.Arg(0), instances)
	if inst == nil {
		out.Error(errMsg, errCode)
		os.Exit(1)
	}

	res := inst.AcceptTrustDialog()
	data := map[string]interface{}{
		"id":      inst.ID,
		"outcome": string(res.Outcome),
		"tool":    res.Tool,
		"path":    res.Path,
		"detail":  res.Detail,
		"roots":   session.TrustAutoAcceptRoots,
	}
	switch res.Outcome {
	case session.TrustAccepted, session.TrustNotPresent:
		data["success"] = true
		out.Success(fmt.Sprintf("trust dialog: %s", res.Outcome), data)
	default:
		out.ErrorWithData(fmt.Sprintf("trust dialog %s: %s", res.Outcome, res.Detail), ErrCodeInvalidOperation, data)
		os.Exit(1)
	}
}

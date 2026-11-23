package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// handleSessionSkipCodexUpdate implements `agent-deck session
// skip-codex-update <id|title> [--json]` (WO-DECK-CODEX-DELIVERY-01): answer
// a Codex session's startup update prompt with Skip so the session becomes
// ready. It acts only on the complete prompt, presses only Escape (which the
// prompt defines as "esc skip"), verifies the prompt closed, and never chooses
// "Update now". Outcomes: skipped, not_present, failed.
func handleSessionSkipCodexUpdate(profile string, args []string) {
	fs := flag.NewFlagSet("session skip-codex-update", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session skip-codex-update <id|title> [--json]")
		fmt.Println()
		fmt.Println("Skip a Codex startup update prompt on the session's pane with Escape (\"esc skip\").")
		fmt.Println("Never installs the update. Outcomes: skipped, not_present, failed.")
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
	data := map[string]interface{}{"id": inst.ID, "tool": inst.Tool}
	if !session.IsCodexCompatible(inst.Tool) {
		data["outcome"] = "not_present"
		data["success"] = true
		out.Success("not a Codex session: no update prompt", data)
		return
	}
	tmuxSess := inst.GetTmuxSession()
	if tmuxSess == nil || !tmuxSess.Exists() {
		data["outcome"] = "failed"
		out.ErrorWithData("session pane is not running", ErrCodeInvalidOperation, data)
		os.Exit(1)
	}
	raw, err := tmuxSess.CapturePaneFresh()
	if err != nil {
		data["outcome"] = "failed"
		out.ErrorWithData(fmt.Sprintf("cannot read the pane: %v", err), ErrCodeInvalidOperation, data)
		os.Exit(1)
	}
	if _, ok := send.IsCodexUpdatePrompt(tmux.StripANSI(raw)); !ok {
		data["outcome"] = "not_present"
		data["success"] = true
		out.Success("no Codex update prompt on the pane", data)
		return
	}
	dialog, skipped, err := skipCodexUpdatePrompt(tmuxSess, codexUpdateSkipVerify)
	data["from"], data["to"] = dialog.From, dialog.To
	if !skipped {
		data["outcome"] = "failed"
		detail := "the update prompt skip is disabled (AGENTDECK_CODEX_UPDATE_SKIP=0)"
		if err != nil {
			detail = err.Error()
		}
		out.ErrorWithData(detail, ErrCodeInvalidOperation, data)
		os.Exit(1)
	}
	data["outcome"] = "skipped"
	data["success"] = true
	out.Success(fmt.Sprintf("skipped the Codex update prompt (%s → %s); nothing was installed", dialog.From, dialog.To), data)
}

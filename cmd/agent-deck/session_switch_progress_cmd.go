package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func handleSessionSwitchProgress(profile string, args []string) {
	fs := flag.NewFlagSet("session switch-progress", flag.ExitOnError)
	operationID := fs.String("operation-id", "", "Exact switch operation ID; omitted reads current receipt")
	target := fs.String("to-account", "", "Target account")
	step := fs.String("step", "waiting_for_idle", "Named workflow step")
	status := fs.String("status", "running", "running, pending, success or failed")
	reason := fs.String("reason", "", "Human failure or pending reason")
	_ = fs.Bool("json", false, "JSON output")
	_ = fs.Parse(normalizeArgs(fs, args))
	out := NewCLIOutput(true, false)
	if fs.NArg() != 1 {
		out.Error("requires one exact Deck session ID", ErrCodeInvalidOperation)
		return
	}
	_, instances, _, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		return
	}
	inst, msg, code := ResolveSession(fs.Arg(0), instances)
	if inst == nil {
		out.Error(msg, code)
		return
	}
	var p *session.HarnessSwitchProgress
	if *operationID == "" {
		p, err = session.ReadHarnessSwitchProgress(inst.ID)
	} else {
		p, err = session.RecordHarnessSwitchProgress(inst, *operationID, *target, *step, *status, *reason)
	}
	if err != nil {
		out.Error(err.Error(), ErrCodeInvalidOperation)
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(p)
}

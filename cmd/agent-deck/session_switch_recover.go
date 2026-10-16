package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// handleSessionSwitchRecover acknowledges a committed native switch journal
// when lifecycle work finished but registry/journal persistence failed.
func handleSessionSwitchRecover(profile string, args []string) {
	fs := flag.NewFlagSet("session switch-recover", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "Output the recovery result as JSON")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session switch-recover <id|title>")
		fmt.Println()
		fmt.Println("Complete the registry acknowledgement for a committed native account/harness switch")
		fmt.Println("when lifecycle work finished but persistence failed (recovery_required).")
		fmt.Println("Does not replay stop/start or transcript migration.")
		fs.PrintDefaults()
	}
	if err := parseCLIFlags(fs, normalizeArgs(fs, args)); err != nil {
		exitCLI(1)
	}
	if fs.NArg() < 1 {
		fs.Usage()
		exitCLI(1)
	}
	out := NewCLIOutput(*jsonOutput, false)
	storage, instances, _, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		exitCLI(1)
	}
	inst, errMsg, errCode := ResolveSession(fs.Arg(0), instances)
	if inst == nil {
		out.Error(errMsg, errCode)
		exitCLI(1)
	}
	result, recoverErr := session.RecoverNativeHarnessSwitch(inst, storage)
	if recoverErr != nil {
		out.Error(recoverErr.Error(), ErrCodeInvalidOperation)
		exitCLI(1)
	}
	payload := map[string]any{
		"success":      true,
		"status":       "recovered",
		"id":           inst.ID,
		"old_account":  result.OldAccount,
		"new_account":  result.NewAccount,
		"conversation": result.Conversation,
	}
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(payload)
		return
	}
	fmt.Printf("Recovered native switch for %s: %s -> %s\n", inst.Title, result.OldAccount, result.NewAccount)
}

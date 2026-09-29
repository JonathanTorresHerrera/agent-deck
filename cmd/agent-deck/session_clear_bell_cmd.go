package main

// Patch 27: `agent-deck session clear-bell` marks the 🔔 "notified · waiting
// for reply" bell (tool_data.last_aida_ask, patches 21/24) handled by hand.
// Used by the agent-deck-mcp tool deck_clear_notification (Aida) and its
// auto-clear after a delivered deck_send_message; JT's in-TUI equivalent is
// Ctrl+X in the B dialog.
//
// The write is one conditional json_set (statedb.WriteLastAidaAskIfUnanswered):
// it lands only while the stored ask still has the same ref and no answer, so
// a newer `B` ask or an answer that arrived first always wins. A running TUI
// picks the change up through its storage watcher, and its full saves are a
// three-way tool_data merge, so they never write the stale bell back.

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

var clearBellByPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

// clearBellNow is a seam for tests.
var clearBellNow = time.Now

func handleSessionClearBell(profile string, args []string) {
	fs := flag.NewFlagSet("session clear-bell", flag.ExitOnError)
	fs.SetOutput(os.Stdout)
	note := fs.String("note", "", fmt.Sprintf("Optional note stored with the clear (max %d chars)", session.AidaAskNoteMax))
	by := fs.String("by", "cli", "Who cleared it (e.g. aida, jt)")
	reason := fs.String("reason", "manual", "Why: manual or message")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	quiet := fs.Bool("q", false, "Quiet mode")

	fs.Usage = func() {
		fmt.Println("Usage: agent-deck session clear-bell <id|title> [options]")
		fmt.Println()
		fmt.Println("Mark the session's Ask-Aida bell (\"notified · waiting for reply\") handled.")
		fmt.Println("Idempotent: result is cleared, already_answered or no_bell.")
		fmt.Println()
		fmt.Println("Options:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		os.Exit(1)
	}
	out := NewCLIOutput(*jsonOutput, *quiet)
	if fs.NArg() != 1 {
		fs.Usage()
		out.Error("exactly one session is required", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	if !clearBellByPattern.MatchString(*by) {
		out.Error("--by must be a short name (letters, digits, . _ @ -)", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	if *reason != "manual" && *reason != "message" {
		out.Error("--reason must be manual or message", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	if msg := validateClearBellNote(*note); msg != "" {
		out.Error(msg, ErrCodeInvalidOperation)
		os.Exit(1)
	}

	storage, instances, _, err := loadSessionData(profile)
	if err != nil {
		out.Error(err.Error(), ErrCodeNotFound)
		os.Exit(1)
	}
	inst, errMsg, errCode := ResolveSession(fs.Arg(0), instances)
	if inst == nil {
		out.Error(errMsg, errCode)
		if errCode == ErrCodeNotFound {
			os.Exit(2)
		}
		os.Exit(1)
		return
	}

	data := map[string]interface{}{"success": true, "session_id": inst.ID, "title": inst.Title}
	rec, ok := inst.LastAidaAsk()
	if !ok {
		data["result"] = "no_bell"
		out.Success(fmt.Sprintf("'%s' has no Ask-Aida bell", inst.Title), data)
		return
	}
	if rec.IsAnswered() {
		data["result"] = "already_answered"
		addClearBellRecord(data, rec)
		out.Success(fmt.Sprintf("'%s' was already answered", inst.Title), data)
		return
	}

	cleared := rec.WithCleared(clearBellNow(), *by, *reason, *note)
	db := storage.GetDB()
	if db == nil {
		out.Error("state database unavailable", ErrCodeInvalidOperation)
		os.Exit(1)
	}
	changed, err := db.WriteLastAidaAskIfUnanswered(inst.ID, rec.Ref, cleared.JSON())
	if err != nil {
		out.Error(fmt.Sprintf("failed to clear the bell: %v", err), ErrCodeInvalidOperation)
		os.Exit(1)
	}
	if !changed {
		// Raced: a new ask replaced the record, or an answer landed first. Either
		// way this bell is no longer the one we were asked to clear.
		data["result"] = "already_answered"
		out.Success(fmt.Sprintf("'%s' changed meanwhile; nothing cleared", inst.Title), data)
		return
	}
	data["result"] = "cleared"
	addClearBellRecord(data, cleared)
	out.Success(fmt.Sprintf("Cleared the Aida bell on '%s'", inst.Title), data)
}

// validateClearBellNote returns an error message, or "" when the note is fine:
// at most AidaAskNoteMax characters and no control characters.
func validateClearBellNote(note string) string {
	if len([]rune(note)) > session.AidaAskNoteMax {
		return fmt.Sprintf("--note is longer than %d characters", session.AidaAskNoteMax)
	}
	if strings.IndexFunc(note, unicode.IsControl) >= 0 {
		return "--note must not contain control characters or newlines"
	}
	return ""
}

func addClearBellRecord(data map[string]interface{}, rec session.AidaAsk) {
	data["ref"] = rec.Ref
	if at, ok := rec.Answered(); ok {
		data["answered_at"] = at.Unix()
	}
	if rec.ClearedBy != "" {
		data["cleared_by"] = rec.ClearedBy
	}
	if rec.ClearReason != "" {
		data["clear_reason"] = rec.ClearReason
	}
	if rec.Note != "" {
		data["note"] = rec.Note
	}
}

// aidaAskJSON is `session show --json`'s "aida_ask" object (patch 27), or nil
// when the session was never asked.
func aidaAskJSON(inst *session.Instance, now time.Time) map[string]interface{} {
	rec, ok := inst.LastAidaAsk()
	if !ok {
		return nil
	}
	m := map[string]interface{}{
		"asked_at":     rec.At.Unix(),
		"status":       rec.Status,
		"ref":          rec.Ref,
		"channel":      rec.Channel,
		"answered_at":  nil,
		"cleared_by":   nil,
		"clear_reason": nil,
		"waiting":      rec.AwaitingReply(now),
	}
	if at, ok := rec.Answered(); ok {
		m["answered_at"] = at.Unix()
	}
	if rec.ClearedBy != "" {
		m["cleared_by"] = rec.ClearedBy
	}
	if rec.ClearReason != "" {
		m["clear_reason"] = rec.ClearReason
	}
	return m
}

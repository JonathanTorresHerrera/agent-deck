package session

import (
	"encoding/json"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// CodexLastTurnError returns the error the newest turn of this Codex session
// ended with, read from its own rollout, or "" when that turn succeeded, is
// still running, or no rollout exists yet. A turn the API rejects (400
// invalid_request_error, e.g. a model a ChatGPT-login account cannot use)
// ends in milliseconds and leaves an idle composer, so the pane alone reads
// as ready; the rollout records the rejection as task_complete.error.
//
// Newest signal wins: a later task_started clears an older error, so a
// session that recovered (new model, resumed turn) does not stay failed.
func (i *Instance) CodexLastTurnError() string {
	if !IsCodexCompatible(i.Tool) {
		return ""
	}
	lines, err := i.codexRolloutTail()
	if err != nil {
		return ""
	}
	return codexLastTurnErrorFromLines(lines)
}

func codexLastTurnErrorFromLines(lines []string) string {
	for k := len(lines) - 1; k >= 0; k-- {
		var record codexRolloutRecord
		if json.Unmarshal([]byte(lines[k]), &record) != nil || record.Type != "event_msg" {
			continue
		}
		switch record.Payload.Type {
		case "task_started", "turn_started":
			return ""
		case "task_complete", "turn_complete":
			if record.Payload.Error == nil || strings.TrimSpace(record.Payload.Error.Message) == "" {
				return ""
			}
			return tmux.CodexRejectionDetail(record.Payload.Error.Message)
		}
	}
	return ""
}

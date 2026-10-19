package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/agentpaths"
)

// SwitchAttempt is one terminal receipt per invocation, separate from the
// recoverable operation journal (which can be reused across many retries).
// It contains identifiers/metadata only, never launch commands or credentials.
type SwitchAttempt struct {
	ID         string    `json:"attempt_id"`
	Timestamp  time.Time `json:"ts"`
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title"`
	Tool       string    `json:"tool"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	TargetTool string    `json:"target_tool"`
	Trigger    string    `json:"trigger"`
	Step       string    `json:"step"`
	Outcome    string    `json:"outcome"`
	Error      string    `json:"error"`
}

type SwitchAttemptError struct {
	AttemptID string
	Cause     error
}

func (e *SwitchAttemptError) Error() string {
	return fmt.Sprintf("Account switch failed (log %s): %v", e.AttemptID, e.Cause)
}
func (e *SwitchAttemptError) Unwrap() error { return e.Cause }
func SwitchAttemptID(err error) string {
	var e *SwitchAttemptError
	if errors.As(err, &e) {
		return e.AttemptID
	}
	return ""
}

func NewSwitchAttempt(inst *Instance, target SwitchPreviewTarget, trigger string) *SwitchAttempt {
	var nonce [16]byte
	// crypto/rand.Read terminates on entropy failure in supported Go versions.
	_, _ = rand.Read(nonce[:])
	a := &SwitchAttempt{ID: hex.EncodeToString(nonce[:]), Timestamp: time.Now().UTC(), To: target.Account, TargetTool: target.Harness, Trigger: trigger, Step: "preflight"}
	if inst != nil {
		a.SessionID, a.Title, a.Tool, a.From = inst.ID, inst.Title, inst.Tool, inst.Account
	}
	if a.Trigger == "" {
		a.Trigger = os.Getenv("AGENTDECK_SWITCH_TRIGGER")
		if a.Trigger == "" {
			if os.Getenv("JEV_ROUTER_HOME") != "" {
				a.Trigger = "rotator"
			} else {
				a.Trigger = "cli"
			}
		}
	}
	return a
}

func (a *SwitchAttempt) Finish(outcome, step string, cause error) error {
	a.Outcome, a.Step = outcome, step
	if cause != nil {
		a.Error = cause.Error()
	}
	root, err := agentpaths.LegacyDir()
	if err == nil {
		err = os.MkdirAll(root, 0700)
		if err == nil {
			path := filepath.Join(root, "switch-attempts.jsonl")
			var lock *ConfigFileLock
			lock, err = AcquireConfigFileLock(path)
			if err == nil {
				defer lock.Release()
				var f *os.File
				f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err == nil {
					data, marshalErr := json.Marshal(a)
					if marshalErr == nil {
						_, err = f.Write(append(data, '\n'))
					} else {
						err = marshalErr
					}
					if err == nil {
						err = f.Sync()
					}
					err = errors.Join(err, f.Close())
				}
			}
		}
	}
	if err != nil {
		cause = errors.Join(cause, fmt.Errorf("persist switch attempt receipt: %w", err))
	}
	if cause != nil {
		return &SwitchAttemptError{AttemptID: a.ID, Cause: cause}
	}
	return nil
}

// RecordSwitchFailure covers adapter refusals before the executor is entered.
func RecordSwitchFailure(inst *Instance, target SwitchPreviewTarget, trigger, step string, cause error) error {
	return NewSwitchAttempt(inst, target, trigger).Finish("failed", step, cause)
}

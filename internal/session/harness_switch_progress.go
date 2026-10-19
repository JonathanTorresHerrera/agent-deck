package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var activeHarnessSwitchProgress sync.Map // instance ID -> lifecycle callback; source lock owns registration.

func notifyHarnessSwitchLifecycle(inst *Instance, step string) {
	if callback, ok := activeHarnessSwitchProgress.Load(inst.ID); ok {
		callback.(func(string))(step)
	}
}

type HarnessSwitchStep struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type HarnessSwitchProgress struct {
	AttemptID      string              `json:"attempt_id"`
	OperationID    string              `json:"operation_id"`
	InstanceID     string              `json:"instance_id"`
	Title          string              `json:"title"`
	TargetAccount  string              `json:"target_account"`
	FinalAccount   string              `json:"final_account"`
	Status         string              `json:"status"`
	Steps          []HarnessSwitchStep `json:"steps"`
	FailedStep     string              `json:"failed_step,omitempty"`
	Reason         string              `json:"reason,omitempty"`
	Sequence       uint64              `json:"seq"`
	LatestLogEntry string              `json:"latest_log_entry"`
	LogPath        string              `json:"log_path"`
	JournalPath    string              `json:"journal_path"`
	ProgressPath   string              `json:"progress_path"`
	UpdatedAt      time.Time           `json:"updated_at"`
	Warnings       []string            `json:"warnings,omitempty"`
}

type HarnessSwitchFailure struct {
	Progress *HarnessSwitchProgress
	Cause    error
}

func (e *HarnessSwitchFailure) Error() string { return e.Cause.Error() }
func (e *HarnessSwitchFailure) Unwrap() error { return e.Cause }

func HarnessSwitchProgressFor(result *HarnessSwitchResult, err error) *HarnessSwitchProgress {
	if result != nil && result.Progress != nil {
		return result.Progress
	}
	var failure *HarnessSwitchFailure
	if errors.As(err, &failure) {
		return failure.Progress
	}
	return nil
}

func ReadHarnessSwitchProgress(id string) (*HarnessSwitchProgress, error) {
	if !validSwitchIdentity(id) {
		return nil, fmt.Errorf("invalid switch instance ID")
	}
	path, err := runtimeDataPath(filepath.Join("harness-switch-progress", switchOperationID(id)+".json"))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p HarnessSwitchProgress
	if err = json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ExecuteHarnessSwitch holds the source lock through progress, lifecycle and
// registry work. The receipt lives outside the restarted harness, and survives
// TUI/CLI recreation. A failed lock never overwrites an active operation.
func ExecuteHarnessSwitch(cfg *UserConfig, inst *Instance, opts HarnessSwitchOptions) (result *HarnessSwitchResult, err error) {
	attempt := NewSwitchAttempt(inst, opts.Target, opts.Trigger)
	external := opts.AttemptID != ""
	if external {
		attempt.ID = opts.AttemptID
	}
	defer func() {
		outcome, step := "pending", "preflight"
		if result != nil {
			result.AttemptID = attempt.ID
			if result.DestinationReady {
				outcome = "success"
			}
			if result.Progress != nil {
				step = result.Progress.activeStep()
			}
		}
		if p := HarnessSwitchProgressFor(result, err); p != nil {
			p.AttemptID = attempt.ID
			step = p.activeStep()
		}
		if err != nil {
			outcome = "failed"
		}
		if external {
			if err != nil {
				err = &SwitchAttemptError{AttemptID: attempt.ID, Cause: err}
			}
		} else {
			err = attempt.Finish(outcome, step, err)
		}
	}()
	if inst == nil || !validSwitchIdentity(inst.ID) {
		return nil, fmt.Errorf("session has no safe switch identity")
	}
	lock, err := acquireHarnessSwitchSourceLock(inst.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSwitchBusy, err)
	}
	defer lock.Release()
	p, err := newHarnessSwitchProgress(inst, opts)
	if err != nil {
		return nil, err
	}
	p.AttemptID = attempt.ID
	opts.progress = p
	activeHarnessSwitchProgress.Store(inst.ID, func(step string) { setSwitchStep(opts, step) })
	defer activeHarnessSwitchProgress.Delete(inst.ID)
	setSwitchStep(opts, "waiting_for_idle")
	defer func() {
		if err == nil && result != nil && result.Committed && opts.Storage != nil {
			err = opts.Storage.CommitNativeHarnessSwitch(inst, result)
		}
		p.FinalAccount = inst.Account
		if err != nil {
			p.Status, p.Reason = "failed", err.Error()
			for i := range p.Steps {
				if p.Steps[i].Status == "running" {
					p.Steps[i].Status = "failed"
					p.FailedStep = p.Steps[i].ID
					now := time.Now()
					p.Steps[i].CompletedAt = &now
				}
			}
			if p.FailedStep == "" {
				p.FailedStep = "switching_account"
			}
		} else if result != nil && result.Committed && result.DestinationReady {
			p.Status = "success"
		} else {
			p.Status = "pending"
			p.Reason = "Account committed; native destination readiness remains pending"
			if result != nil && result.Conversation != "" {
				p.Reason = result.Conversation
			}
		}
		now := time.Now()
		for i := range p.Steps {
			if p.Status == "success" && p.Steps[i].Status == "pending" {
				p.Steps[i].Status = "skipped"
				p.Steps[i].CompletedAt = &now
			}
			if p.Steps[i].Status == "running" && p.Status == "success" {
				p.Steps[i].Status = "completed"
				p.Steps[i].CompletedAt = &now
			}
			if opts.NoStart && (p.Steps[i].ID == "resuming" || p.Steps[i].ID == "verifying") {
				p.Steps[i].Status = "skipped"
				p.Steps[i].CompletedAt = &now
			}
		}
		if result != nil {
			result.Progress = p
			result.Warnings = append(result.Warnings, p.Warnings...)
		}
		p.publish(opts.Progress)
		if err != nil {
			err = &HarnessSwitchFailure{Progress: p, Cause: err}
		}
	}()
	return executeHarnessSwitch(cfg, inst, opts)
}

func displaySwitchAccount(account string) string {
	if account == "" {
		return "default"
	}
	return account
}

func setSwitchStep(opts HarnessSwitchOptions, id string) {
	p := opts.progress
	if p == nil {
		return
	}
	now := time.Now()
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.Status == "running" {
			s.Status = "completed"
			s.CompletedAt = &now
		}
		if s.ID == id {
			s.Status = "running"
			if s.StartedAt == nil {
				s.StartedAt = &now
			}
			s.CompletedAt = nil
		}
	}
	p.publish(opts.Progress)
}

func (p *HarnessSwitchProgress) publish(callback func(HarnessSwitchProgress)) {
	p.Sequence++
	p.UpdatedAt = time.Now()
	entry, _ := json.Marshal(struct {
		OperationID string    `json:"operation_id"`
		Seq         uint64    `json:"seq"`
		Status      string    `json:"status"`
		Step        string    `json:"step"`
		Reason      string    `json:"reason,omitempty"`
		At          time.Time `json:"at"`
	}{p.OperationID, p.Sequence, p.Status, p.activeStep(), p.Reason, p.UpdatedAt})
	p.LatestLogEntry = string(entry)
	if err := os.MkdirAll(filepath.Dir(p.LogPath), 0700); err == nil {
		if f, e := os.OpenFile(p.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
			_, _ = f.Write(append(entry, '\n'))
			_ = f.Close()
		}
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p.ProgressPath), 0700); err == nil {
		_ = writeFileDurable(p.ProgressPath, data, 0600)
	}
	if callback != nil {
		snapshot := *p
		snapshot.Steps = append([]HarnessSwitchStep(nil), p.Steps...)
		snapshot.Warnings = append([]string(nil), p.Warnings...)
		callback(snapshot)
	}
}
func (p *HarnessSwitchProgress) activeStep() string {
	for _, s := range p.Steps {
		if s.Status == "running" || s.Status == "failed" {
			return s.ID
		}
	}
	return p.Status
}

func (p *HarnessSwitchProgress) Text() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.Status == "success" {
		fmt.Fprintf(&b, "Switched %s to %s\n", p.Title, displaySwitchAccount(p.FinalAccount))
	} else {
		fmt.Fprintf(&b, "%s -> %s: %s\n", p.Title, displaySwitchAccount(p.TargetAccount), p.Status)
	}
	for n, s := range p.Steps {
		fmt.Fprintf(&b, "%d. %s [%s]\n", n+1, s.Name, s.Status)
	}
	if p.FailedStep != "" {
		fmt.Fprintf(&b, "\nAccount switch failed (log %s)\nFailed step: %s\n", p.AttemptID, p.FailedStep)
	}
	if p.Reason != "" {
		fmt.Fprintf(&b, "\n%s\n", p.Reason)
	}
	if len(p.Warnings) > 0 {
		fmt.Fprintf(&b, "\n%s\n", strings.Join(p.Warnings, "\n"))
	}
	fmt.Fprintf(&b, "\noperation_id: %s\nLatest entry: %s\nJournal: %s\nDeck log: %s\nProgress: %s", p.OperationID, p.LatestLogEntry, p.JournalPath, p.LogPath, p.ProgressPath)
	return b.String()
}

// Fields is shared by CLI and MCP adapters; diagnostics are never clipped.
func (p *HarnessSwitchProgress) Fields() map[string]any {
	if p == nil {
		return map[string]any{}
	}
	data, _ := json.Marshal(p)
	fields := map[string]any{}
	_ = json.Unmarshal(data, &fields)
	return fields
}

func newHarnessSwitchProgress(inst *Instance, opts HarnessSwitchOptions) (*HarnessSwitchProgress, error) {
	path, pathErr := runtimeDataPath(filepath.Join("harness-switch-progress", switchOperationID(inst.ID)+".json"))
	if pathErr != nil {
		return nil, pathErr
	}
	logPath, pathErr := logDataPath("harness-switch.jsonl")
	if pathErr != nil {
		return nil, pathErr
	}
	request := switchRequestGeneration(identityForInstance(inst), opts.Target, opts.NoStart)
	p := &HarnessSwitchProgress{OperationID: switchOperationIDForRequest(inst.ID, request), InstanceID: inst.ID, Title: inst.Title, TargetAccount: opts.Target.Account, FinalAccount: inst.Account, Status: "running", LogPath: logPath, JournalPath: path, ProgressPath: path}
	for _, s := range [][2]string{{"waiting_for_idle", "Waiting for idle"}, {"stopping", "Stopping (graceful exit)"}, {"resolve_session_id", "Resolving session ID"}, {"switching_account", "Switching account -> " + displaySwitchAccount(opts.Target.Account)}, {"resuming", "Resuming"}, {"verifying", "Verifying the new account is active"}} {
		p.Steps = append(p.Steps, HarnessSwitchStep{ID: s[0], Name: s[1], Status: "pending"})
	}
	if opts.OperationID != "" {
		if !validSwitchIdentity(opts.OperationID) || len(opts.OperationID) > 200 {
			return nil, fmt.Errorf("invalid switch operation ID")
		}
		p.OperationID = opts.OperationID
		if prior, err := ReadHarnessSwitchProgress(inst.ID); err == nil && prior.OperationID == opts.OperationID {
			if prior.TargetAccount != opts.Target.Account {
				return nil, fmt.Errorf("switch progress target changed")
			}
			return prior, nil
		}
	}
	return p, nil
}

// RecordHarnessSwitchProgress joins MCP's graceful-exit and readiness phases
// to the same receipt used by the native executor. It never changes a session
// account, starts a process, or installs/trusts hooks.
func RecordHarnessSwitchProgress(inst *Instance, operationID, targetAccount, step, status, reason string) (*HarnessSwitchProgress, error) {
	if inst == nil || !validSwitchIdentity(inst.ID) || operationID == "" {
		return nil, fmt.Errorf("progress requires exact session and operation IDs")
	}
	lock, err := acquireHarnessSwitchSourceLock(inst.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSwitchBusy, err)
	}
	defer lock.Release()
	opts := HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: inst.Tool, Account: targetAccount}, OperationID: operationID}
	p, err := newHarnessSwitchProgress(inst, opts)
	if err != nil {
		return nil, err
	}
	if prior, readErr := ReadHarnessSwitchProgress(inst.ID); readErr == nil && prior.OperationID != operationID && prior.Status == "running" && time.Since(prior.UpdatedAt) < 10*time.Minute {
		return nil, ErrSwitchBusy
	}
	validStep := false
	for _, s := range p.Steps {
		if s.ID == step {
			validStep = true
		}
	}
	if !validStep {
		return nil, fmt.Errorf("unknown switch progress step %q", step)
	}
	switch status {
	case "running", "pending", "success", "failed":
	default:
		return nil, fmt.Errorf("invalid switch progress status")
	}
	if status == "success" && inst.Account != targetAccount {
		return nil, fmt.Errorf("cannot report success: target account is not committed")
	}
	opts.progress = p
	p.Status = "running"
	setSwitchStep(opts, step)
	p.Status, p.Reason, p.FinalAccount = status, reason, inst.Account
	if status == "failed" {
		p.FailedStep = step
	}
	if status == "success" || status == "failed" {
		now := time.Now()
		for i := range p.Steps {
			if status == "success" && p.Steps[i].Status == "pending" {
				p.Steps[i].Status = "skipped"
				p.Steps[i].CompletedAt = &now
			}
			if p.Steps[i].Status == "running" {
				p.Steps[i].Status = "completed"
				if status == "failed" {
					p.Steps[i].Status = "failed"
				}
				p.Steps[i].CompletedAt = &now
			}
		}
	}
	p.publish(nil)
	return p, nil
}

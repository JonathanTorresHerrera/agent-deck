package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// Version 2 (WO-DECK-CODEX-DELIVERY-01) adds rollout_offset. Version 1
	// markers written by earlier builds stay readable and reconcilable.
	codexSubmissionMarkerVersionV1 = 1
	codexSubmissionMarkerVersion   = 2
	codexSubmissionMarkerMaxSize   = 8 << 10

	CodexSubmissionPhasePrepared  = "prepared"
	CodexSubmissionPhaseSubmitted = "submitted"
	codexSubmissionPhaseAmbiguous = "transport_ambiguous"
)

// CodexSubmissionMarker is the content-free durable handoff between
// cooperating session-send processes. It records only which acceptance fence
// a send owns; prompts, responses, and their hashes never enter this file.
type CodexSubmissionMarker struct {
	Version             int       `json:"version"`
	InstanceID          string    `json:"instance_id"`
	CodexSessionID      string    `json:"codex_session_id"`
	AttemptID           string    `json:"attempt_id"`
	PriorTurnGeneration string    `json:"prior_turn_generation"`
	Phase               string    `json:"phase"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	// RolloutOffset (version 2) is the exact rollout's length, at a line
	// boundary, when the fence was captured; 0 when no rollout existed yet.
	// Delivery evidence for this attempt is searched only after it. It is a
	// byte count, so the marker stays content-free. Absent on version 1.
	RolloutOffset *int64 `json:"rollout_offset,omitempty"`
}

// PrepareCodexSubmissionMarker durably publishes an unresolved attempt before
// any bytes are sent to the pane. The caller must hold the matching acceptance
// lock from before reconciliation until this write completes. rolloutOffset is
// the fence's rollout length (CodexTurnState.RolloutOffset).
func PrepareCodexSubmissionMarker(instanceID, codexSessionID, priorGeneration string, rolloutOffset int64, now time.Time) (*CodexSubmissionMarker, error) {
	instanceID = strings.TrimSpace(instanceID)
	codexSessionID = strings.TrimSpace(codexSessionID)
	if instanceID == "" || len(instanceID) > 512 {
		return nil, fmt.Errorf("Codex submission marker: invalid instance id")
	}
	if err := validateExactSessionID(codexSessionID); err != nil {
		return nil, fmt.Errorf("Codex submission marker: invalid session identity: %w", err)
	}
	if err := validateCodexMarkerGeneration(codexSessionID, priorGeneration, true); err != nil {
		return nil, err
	}
	if rolloutOffset < 0 {
		return nil, fmt.Errorf("Codex submission marker: invalid rollout offset")
	}
	attemptID, err := newCodexSubmissionAttemptID()
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	marker := &CodexSubmissionMarker{
		Version:             codexSubmissionMarkerVersion,
		InstanceID:          instanceID,
		CodexSessionID:      codexSessionID,
		AttemptID:           attemptID,
		PriorTurnGeneration: priorGeneration,
		Phase:               CodexSubmissionPhasePrepared,
		CreatedAt:           now,
		UpdatedAt:           now,
		RolloutOffset:       &rolloutOffset,
	}
	if err := writeCodexSubmissionMarker(marker, true); err != nil {
		return nil, err
	}
	return marker, nil
}

// MarkSubmitted records positive transport-level submission evidence. A crash
// before this update safely leaves the more conservative prepared phase.
func (m *CodexSubmissionMarker) MarkSubmitted(now time.Time) error {
	return m.updatePhase(CodexSubmissionPhaseSubmitted, now)
}

// MarkTransportAmbiguous records that transport was attempted without exact
// submission evidence. It deliberately remains unresolved until a new rollout
// generation appears or an operator proves transport never occurred.
func (m *CodexSubmissionMarker) MarkTransportAmbiguous(now time.Time) error {
	return m.updatePhase(codexSubmissionPhaseAmbiguous, now)
}

func (m *CodexSubmissionMarker) updatePhase(phase string, now time.Time) error {
	if m == nil {
		return fmt.Errorf("Codex submission marker: nil marker")
	}
	if now.IsZero() {
		now = time.Now()
	}
	next := *m
	if m.RolloutOffset != nil {
		offset := *m.RolloutOffset
		next.RolloutOffset = &offset
	}
	next.Phase = phase
	next.UpdatedAt = now.UTC()
	if err := writeCodexSubmissionMarker(&next, false); err != nil {
		return err
	}
	*m = next
	return nil
}

// ErrUnresolvedCodexSubmission marks an earlier attempt whose delivery the
// exact rollout cannot prove: a later send would risk a double submission.
var ErrUnresolvedCodexSubmission = errors.New("unresolved Codex submission")

// ErrCodexMarkerOwnerMismatch marks a marker that belongs to another
// instance or thread than the sender's.
var ErrCodexMarkerOwnerMismatch = errors.New("Codex submission marker ownership does not match this instance")

// CodexDeliveryEvidenceFunc searches the exact rollout for proof that an
// attempt cut at (cutOffset, notBefore) reached Codex; cutOffset < 0 means
// the cut is unknown (a version 1 marker). nil evidence means no proof.
type CodexDeliveryEvidenceFunc func(cutOffset int64, notBefore time.Time) (*CodexDeliveryEvidence, error)

// CodexMarkerResolution reports how an earlier attempt's marker was resolved.
type CodexMarkerResolution struct {
	AttemptID  string                 `json:"attempt_id"`
	Phase      string                 `json:"phase"`
	Evidence   *CodexDeliveryEvidence `json:"evidence"`
	ArchivedTo string                 `json:"archived_to"`
}

// ReconcileCodexSubmissionMarker is ReconcileCodexSubmissionMarkerWithEvidence
// without rollout evidence: only a changed exact generation resolves.
func ReconcileCodexSubmissionMarker(instanceID, codexSessionID, currentGeneration string) (string, error) {
	resolution, err := ReconcileCodexSubmissionMarkerWithEvidence(instanceID, codexSessionID, currentGeneration, nil, time.Now())
	if err != nil || resolution == nil {
		return "", err
	}
	if resolution.Evidence != nil && resolution.Evidence.TurnGeneration != "" {
		return resolution.Evidence.TurnGeneration, nil
	}
	return currentGeneration, nil
}

// ReconcileCodexSubmissionMarkerWithEvidence resolves an earlier sender's
// attempt (crashed, or a non-wait send that left its marker) while the
// matching acceptance lock is held. The attempt is resolved by:
//
//   - a changed exact generation, when the marker recorded a real prior
//     generation (a "" prior can be the old 8 MiB scan window's false
//     "no turn", so it never resolves this way); or
//   - delivery evidence in the exact rollout after the attempt's cut: a turn
//     that started after it, or input folded into a running turn
//     (WO-DECK-CODEX-DELIVERY-01: Codex 0.161 folds mid-turn input and
//     starts no new turn, which used to wedge every later send).
//
// A resolved marker is ARCHIVED (moved, never deleted) with a content-free
// manifest line naming the evidence. Without proof the send fails closed
// with ErrUnresolvedCodexSubmission: delivery is unknowable and a second
// send could double-submit.
//
// Safety: a false positive (evidence that was not this attempt's input)
// unblocks the next send while this attempt's own outcome stays what its
// sender already reported (ambiguous). It can lose this attempt's text; it
// can never type it twice.
func ReconcileCodexSubmissionMarkerWithEvidence(
	instanceID, codexSessionID, currentGeneration string,
	evidence CodexDeliveryEvidenceFunc,
	now time.Time,
) (*CodexMarkerResolution, error) {
	marker, found, err := readCodexSubmissionMarker(codexSessionID)
	if err != nil || !found {
		return nil, err
	}
	if marker.InstanceID != strings.TrimSpace(instanceID) || marker.CodexSessionID != strings.TrimSpace(codexSessionID) {
		return nil, ErrCodexMarkerOwnerMismatch
	}
	path, err := codexSubmissionMarkerPath(codexSessionID)
	if err != nil {
		return nil, err
	}
	var proof *CodexDeliveryEvidence
	if marker.PriorTurnGeneration != "" && currentGeneration != "" && currentGeneration != marker.PriorTurnGeneration {
		if err := validateCodexMarkerGeneration(marker.CodexSessionID, currentGeneration, false); err != nil {
			return nil, fmt.Errorf("cannot reconcile Codex submission marker: %w", err)
		}
		proof = &CodexDeliveryEvidence{Kind: CodexEvidenceNewTurn, Basis: CodexEvidenceBasisGeneration, TurnGeneration: currentGeneration}
	} else if evidence != nil {
		cut := int64(-1)
		if marker.RolloutOffset != nil {
			cut = *marker.RolloutOffset
		}
		proof, err = evidence(cut, marker.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w %s at %s: the exact rollout could not be read for delivery evidence (%v); refusing a second send",
				ErrUnresolvedCodexSubmission, marker.AttemptID, path, err)
		}
	}
	if proof == nil {
		return nil, fmt.Errorf(
			"%w %s at %s (%s since %s): no turn started and no input landed after it in the exact Codex rollout; refusing a second send. Run `agent-deck codex-markers` to inspect it (dry run); it archives a marker only when its delivery is proven or its session is gone",
			ErrUnresolvedCodexSubmission, marker.AttemptID, path, marker.Phase, marker.CreatedAt.UTC().Format(time.RFC3339),
		)
	}
	if now.IsZero() {
		now = time.Now()
	}
	archived, err := archiveCodexSubmissionMarker(marker, "reconciled-"+now.UTC().Format("20060102"), "reconcile", proof, now)
	if err != nil {
		return nil, err
	}
	return &CodexMarkerResolution{AttemptID: marker.AttemptID, Phase: marker.Phase, Evidence: proof, ArchivedTo: archived}, nil
}

// ClearCodexSubmissionMarker removes only the exact attempt supplied by the
// caller. Callers use this after an exact accepted generation or definitive
// proof that transport never occurred, never after an ambiguous failure.
func ClearCodexSubmissionMarker(marker *CodexSubmissionMarker) error {
	if err := validateCodexSubmissionMarker(marker); err != nil {
		return err
	}
	current, found, err := readCodexSubmissionMarker(marker.CodexSessionID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Codex submission marker %s is missing", marker.AttemptID)
	}
	if current.InstanceID != marker.InstanceID || current.CodexSessionID != marker.CodexSessionID || current.AttemptID != marker.AttemptID {
		return fmt.Errorf("Codex submission marker ownership changed; refusing removal")
	}
	path, err := codexSubmissionMarkerPath(marker.CodexSessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove Codex submission marker: %w", err)
	}
	if err := fsyncDirStrict(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync Codex submission marker directory: %w", err)
	}
	return nil
}

func newCodexSubmissionAttemptID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create Codex submission attempt id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func codexSubmissionMarkerDir() (string, error) {
	locks, err := resolveLocksDirForSpawnLock()
	if err != nil {
		return "", fmt.Errorf("resolve Codex submission marker directory: %w", err)
	}
	dir := filepath.Join(locks, "codex-submissions")
	_, statErr := os.Lstat(dir)
	created := os.IsNotExist(statErr)
	if statErr != nil && !created {
		return "", statErr
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create Codex submission marker directory: %w", err)
	}
	if created {
		if err := fsyncDirStrict(filepath.Dir(dir)); err != nil {
			return "", fmt.Errorf("sync Codex submission marker parent directory: %w", err)
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if err := validatePrivateCodexMarkerPath(info, true); err != nil {
		return "", fmt.Errorf("unsafe Codex submission marker directory: %w", err)
	}
	return dir, nil
}

func codexSubmissionMarkerPath(codexSessionID string) (string, error) {
	codexSessionID = strings.TrimSpace(codexSessionID)
	if err := validateExactSessionID(codexSessionID); err != nil {
		return "", fmt.Errorf("Codex submission marker: invalid session identity: %w", err)
	}
	dir, err := codexSubmissionMarkerDir()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(codexSessionID))
	return filepath.Join(dir, hex.EncodeToString(digest[:])+".json"), nil
}

func readCodexSubmissionMarker(codexSessionID string) (*CodexSubmissionMarker, bool, error) {
	path, err := codexSubmissionMarkerPath(codexSessionID)
	if err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open Codex submission marker: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if err := validatePrivateCodexMarkerPath(info, false); err != nil {
		return nil, false, fmt.Errorf("unsafe Codex submission marker: %w", err)
	}
	if info.Size() > codexSubmissionMarkerMaxSize {
		return nil, false, fmt.Errorf("Codex submission marker exceeds %d bytes", codexSubmissionMarkerMaxSize)
	}
	decoder := json.NewDecoder(io.LimitReader(f, codexSubmissionMarkerMaxSize+1))
	decoder.DisallowUnknownFields()
	var marker CodexSubmissionMarker
	if err := decoder.Decode(&marker); err != nil {
		return nil, false, fmt.Errorf("parse Codex submission marker: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, false, fmt.Errorf("parse Codex submission marker: trailing data")
	}
	if err := validateCodexSubmissionMarker(&marker); err != nil {
		return nil, false, err
	}
	if marker.CodexSessionID != strings.TrimSpace(codexSessionID) {
		return nil, false, fmt.Errorf("Codex submission marker session identity does not match its path")
	}
	return &marker, true, nil
}

func writeCodexSubmissionMarker(marker *CodexSubmissionMarker, create bool) error {
	if err := validateCodexSubmissionMarker(marker); err != nil {
		return err
	}
	path, err := codexSubmissionMarkerPath(marker.CodexSessionID)
	if err != nil {
		return err
	}
	current, found, err := readCodexSubmissionMarker(marker.CodexSessionID)
	if err != nil {
		return err
	}
	if create && found {
		return fmt.Errorf("unresolved Codex submission marker already exists")
	}
	if !create && (!found || current.InstanceID != marker.InstanceID || current.AttemptID != marker.AttemptID) {
		return fmt.Errorf("Codex submission marker ownership changed; refusing update")
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codex-submission-*.tmp")
	if err != nil {
		return fmt.Errorf("create Codex submission marker temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync Codex submission marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish Codex submission marker: %w", err)
	}
	if err := fsyncDirStrict(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync Codex submission marker directory: %w", err)
	}
	return nil
}

func validateCodexSubmissionMarker(marker *CodexSubmissionMarker) error {
	if marker == nil {
		return fmt.Errorf("unsupported Codex submission marker version")
	}
	switch marker.Version {
	case codexSubmissionMarkerVersionV1:
		if marker.RolloutOffset != nil {
			return fmt.Errorf("invalid Codex submission marker: version 1 carries no rollout offset")
		}
	case codexSubmissionMarkerVersion:
		if marker.RolloutOffset == nil || *marker.RolloutOffset < 0 {
			return fmt.Errorf("invalid Codex submission marker rollout offset")
		}
	default:
		return fmt.Errorf("unsupported Codex submission marker version")
	}
	if strings.TrimSpace(marker.InstanceID) == "" || marker.InstanceID != strings.TrimSpace(marker.InstanceID) || len(marker.InstanceID) > 512 {
		return fmt.Errorf("invalid Codex submission marker instance identity")
	}
	if err := validateExactSessionID(marker.CodexSessionID); err != nil || marker.CodexSessionID != strings.TrimSpace(marker.CodexSessionID) {
		return fmt.Errorf("invalid Codex submission marker session identity")
	}
	attempt, err := hex.DecodeString(marker.AttemptID)
	if err != nil || len(attempt) != 16 {
		return fmt.Errorf("invalid Codex submission marker attempt identity")
	}
	if err := validateCodexMarkerGeneration(marker.CodexSessionID, marker.PriorTurnGeneration, true); err != nil {
		return err
	}
	switch marker.Phase {
	case CodexSubmissionPhasePrepared, CodexSubmissionPhaseSubmitted, codexSubmissionPhaseAmbiguous:
	default:
		return fmt.Errorf("invalid Codex submission marker lifecycle phase")
	}
	if marker.CreatedAt.IsZero() || marker.UpdatedAt.IsZero() || marker.UpdatedAt.Before(marker.CreatedAt) {
		return fmt.Errorf("invalid Codex submission marker timestamps")
	}
	return nil
}

func validateCodexMarkerGeneration(codexSessionID, generation string, allowEmpty bool) error {
	if generation == "" && allowEmpty {
		return nil
	}
	if generation == "" || len(generation) > 2048 || strings.ContainsRune(generation, '\x00') ||
		!strings.HasPrefix(generation, codexSessionID+":") || len(generation) == len(codexSessionID)+1 {
		return fmt.Errorf("invalid Codex submission marker turn generation")
	}
	return nil
}

func validatePrivateCodexMarkerPath(info os.FileInfo, wantDir bool) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != wantDir || (!wantDir && !info.Mode().IsRegular()) {
		return fmt.Errorf("path has the wrong file type")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("path permissions %o are not private", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("path is not owned by the current user")
	}
	return nil
}

package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Rollout scanning for Codex turn identity and delivery evidence
// (WO-DECK-CODEX-DELIVERY-01).
//
// Two facts about Codex 0.161 rollouts shape this file.
//
//  1. One turn can write far more than any fixed backward window: tool
//     output, reasoning and compaction records. Rollouts of 13-30 MB whose
//     newest turn start sat 13-16 MB before EOF read as "no turn at all"
//     under the old 8 MiB window, so the acceptance fence and every later
//     reconcile saw generation "" and an unresolved marker could never
//     clear. The backward scan here reads back to the newest turn start
//     however far that is, and only JSON-decodes lines a cheap substring
//     prefilter selects.
//
//  2. Input typed while a turn runs is folded into that turn. Codex records
//     it as a user `response_item` (role "user") and starts no new turn. That
//     record is delivery evidence, but only when it is not the running
//     turn's own opening prompt (written 0.1-0.6 s after `task_started`) and
//     not context Codex injects itself ("# AGENTS.md instructions ...",
//     "<environment_context>", other tag-wrapped blocks).
//
// Nothing here stores, hashes or returns prompt or response text: the user
// text is read only to tell an injected context block from typed input.

const (
	codexRolloutScanChunkMin = int64(256 << 10)
	codexRolloutScanChunkMax = int64(8 << 20)
	codexRolloutForwardChunk = int64(1 << 20)
	// codexRolloutScanCeiling bounds one backward scan. A rollout larger
	// than this without a turn start is an error, never "no turn".
	codexRolloutScanCeiling = int64(2 << 30)
	// codexEvidenceClockSlack only absorbs Codex truncating its record
	// timestamps to milliseconds; the Deck and Codex share one clock. It must
	// stay far below the ~0.1-0.6 s between a turn's start and its prompt: a
	// turn that started before the marker is never this attempt's turn.
	codexEvidenceClockSlack = 5 * time.Millisecond
	// codexTimeCutLookback is how far before the time cut the backward
	// search keeps reading: records are written in near-chronological order.
	codexTimeCutLookback = 30 * time.Second
)

var errCodexRolloutScanCeiling = errors.New("Codex rollout exceeds the scan ceiling without a turn start")

// Delivery evidence kinds.
const (
	// CodexEvidenceNewTurn: a turn started after the attempt's cut.
	CodexEvidenceNewTurn = "new_turn"
	// CodexEvidenceInputFolded: user input landed in a turn that was
	// already running (Codex 0.161 folds mid-turn input into the turn).
	CodexEvidenceInputFolded = "input_folded"
)

// Bases a cut can rest on.
const (
	CodexEvidenceBasisOffset     = "rollout_offset"
	CodexEvidenceBasisCreatedAt  = "created_at"
	CodexEvidenceBasisGeneration = "generation"
)

// CodexDeliveryEvidence is content-free proof, read from the exact rollout,
// that a submission attempt reached Codex.
type CodexDeliveryEvidence struct {
	Kind           string    `json:"kind"`
	Basis          string    `json:"basis"`
	RecordOffset   int64     `json:"record_offset,omitempty"`
	RecordTime     time.Time `json:"record_time,omitempty"`
	TurnGeneration string    `json:"turn_generation,omitempty"`
}

// CodexTurnState is what the exact rollout says about the newest turn.
type CodexTurnState struct {
	// Generation is "<thread>:<turn>" of the newest turn start, "" when the
	// rollout has none yet.
	Generation string
	// TurnOpen reports that the newest lifecycle record is a turn start: a
	// turn is running, or ended without a completion record.
	TurnOpen bool
	// PromptSeen reports that the open turn's opening prompt is written.
	PromptSeen bool
	// RolloutPresent is false for a thread whose rollout Codex has not
	// written yet (a fresh composer); RolloutOffset is then 0.
	RolloutPresent bool
	// RolloutOffset is the rollout length up to its last complete line when
	// scanned. Delivery evidence for a send is searched after it.
	RolloutOffset int64
}

type codexRolloutRecordKind int

const (
	codexRecordOther codexRolloutRecordKind = iota
	codexRecordStart
	codexRecordComplete
	codexRecordAborted
	codexRecordUserInput
	codexRecordInjected
)

type codexRolloutRecordInfo struct {
	Kind   codexRolloutRecordKind
	TurnID string
	Time   time.Time
}

var (
	codexNeedleTaskStarted  = []byte(`"task_started"`)
	codexNeedleTurnStarted  = []byte(`"turn_started"`)
	codexNeedleTaskComplete = []byte(`"task_complete"`)
	codexNeedleTurnComplete = []byte(`"turn_complete"`)
	codexNeedleTurnAborted  = []byte(`"turn_aborted"`)
	codexNeedleUser         = []byte(`"user"`)
	codexNeedleUserMessage  = []byte(`"user_message"`)
	codexNeedleTimestamp    = []byte(`"timestamp":"`)
)

type codexRolloutDecoded struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type    string          `json:"type"`
		TurnID  string          `json:"turn_id"`
		Role    string          `json:"role"`
		Message json.RawMessage `json:"message"`
		Content json.RawMessage `json:"content"`
	} `json:"payload"`
}

func codexLineMayMatter(line []byte) bool {
	return bytes.Contains(line, codexNeedleTaskStarted) || bytes.Contains(line, codexNeedleTurnStarted) ||
		bytes.Contains(line, codexNeedleTaskComplete) || bytes.Contains(line, codexNeedleTurnComplete) ||
		bytes.Contains(line, codexNeedleTurnAborted) || bytes.Contains(line, codexNeedleUser) ||
		bytes.Contains(line, codexNeedleUserMessage)
}

// classifyCodexRolloutLine reports what one rollout record means for turn
// identity and delivery. Unknown, malformed and unrelated records are Other.
func classifyCodexRolloutLine(line []byte) codexRolloutRecordInfo {
	if !codexLineMayMatter(line) {
		return codexRolloutRecordInfo{}
	}
	var rec codexRolloutDecoded
	if json.Unmarshal(line, &rec) != nil {
		return codexRolloutRecordInfo{}
	}
	info := codexRolloutRecordInfo{Time: parseCodexRolloutTime(rec.Timestamp)}
	switch {
	case rec.Type == "event_msg" && (rec.Payload.Type == "task_started" || rec.Payload.Type == "turn_started"):
		if rec.Payload.TurnID == "" {
			return codexRolloutRecordInfo{}
		}
		info.Kind, info.TurnID = codexRecordStart, rec.Payload.TurnID
	case rec.Type == "event_msg" && (rec.Payload.Type == "task_complete" || rec.Payload.Type == "turn_complete"):
		info.Kind, info.TurnID = codexRecordComplete, rec.Payload.TurnID
	case rec.Type == "event_msg" && rec.Payload.Type == "turn_aborted":
		info.Kind, info.TurnID = codexRecordAborted, rec.Payload.TurnID
	case rec.Type == "response_item" && rec.Payload.Type == "message" && rec.Payload.Role == "user":
		info.Kind = classifyCodexUserText(firstCodexInputText(rec.Payload.Content))
	case rec.Type == "event_msg" && rec.Payload.Type == "user_message":
		var text string
		_ = json.Unmarshal(rec.Payload.Message, &text)
		info.Kind = classifyCodexUserText(text)
	default:
		return codexRolloutRecordInfo{}
	}
	return info
}

func firstCodexInputText(raw json.RawMessage) string {
	var items []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if item.Text != "" {
				return item.Text
			}
		}
		return ""
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

// A user-role block Codex writes itself opens with a tag ("<environment_context>",
// "<user_instructions>", "<turn_aborted>", ...) or the AGENTS.md header.
var codexInjectedTagRE = regexp.MustCompile(`^<[A-Za-z_][A-Za-z0-9_.:-]*[\s>]`)

func classifyCodexUserText(text string) codexRolloutRecordKind {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(trimmed, "# AGENTS.md instructions") || codexInjectedTagRE.MatchString(trimmed) {
		return codexRecordInjected
	}
	return codexRecordUserInput
}

func parseCodexRolloutTime(stamp string) time.Time {
	if stamp == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// codexLineTime extracts a record's timestamp without decoding the line.
func codexLineTime(line []byte) time.Time {
	i := bytes.Index(line, codexNeedleTimestamp)
	if i < 0 {
		return time.Time{}
	}
	rest := line[i+len(codexNeedleTimestamp):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}
	}
	return parseCodexRolloutTime(string(rest[:j]))
}

// scanCodexRolloutBackward visits complete lines of [0, limit) from the last
// to the first and returns the offset just past the last complete line.
// Bytes after the final newline are visited only when they already form a
// whole JSON record (Codex terminates every record, so this is a record
// being written only when it is not valid JSON, and it is then skipped).
// visit returns false to stop.
func scanCodexRolloutBackward(r io.ReaderAt, limit int64, visit func(offset int64, line []byte) bool) (int64, error) {
	chunk := codexRolloutScanChunkMin
	pos := limit
	end := int64(-1)
	var pending []byte
	var scanned int64
	for pos > 0 {
		n := min(chunk, pos)
		buf := make([]byte, n, n+int64(len(pending)))
		if _, err := r.ReadAt(buf, pos-n); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		pos -= n
		scanned += n
		data := append(buf, pending...)
		pending = nil
		if end < 0 {
			nl := bytes.LastIndexByte(data, '\n')
			if nl < 0 && pos > 0 {
				pending = data
				if scanned > codexRolloutScanCeiling {
					return 0, errCodexRolloutScanCeiling
				}
				chunk = min(chunk*2, codexRolloutScanChunkMax)
				continue
			}
			end = pos + int64(nl) + 1
			if trailing := bytes.TrimSpace(data[nl+1:]); len(trailing) > 0 && json.Valid(trailing) {
				end = limit
				if !visit(pos+int64(nl)+1, data[nl+1:]) {
					return end, nil
				}
			}
			if nl < 0 {
				return end, nil
			}
			data = data[:nl+1]
		}
		segEnd := len(data) - 1
		for {
			nl := bytes.LastIndexByte(data[:segEnd], '\n')
			if nl < 0 {
				if pos == 0 {
					if line := data[:segEnd]; len(bytes.TrimSpace(line)) > 0 && !visit(0, line) {
						return end, nil
					}
				} else {
					pending = append([]byte(nil), data[:segEnd+1]...)
				}
				break
			}
			if line := data[nl+1 : segEnd]; len(bytes.TrimSpace(line)) > 0 && !visit(pos+int64(nl)+1, line) {
				return end, nil
			}
			segEnd = nl
		}
		if scanned > codexRolloutScanCeiling {
			return end, errCodexRolloutScanCeiling
		}
		chunk = min(chunk*2, codexRolloutScanChunkMax)
	}
	if end < 0 {
		end = 0
	}
	return end, nil
}

// scanCodexRolloutForward visits complete lines of [from, size) in file
// order and returns the offset just past the last complete line it read.
func scanCodexRolloutForward(r io.ReaderAt, size, from int64, visit func(offset int64, line []byte) bool) (int64, error) {
	pos := from
	lineStart := from
	var carry []byte
	for pos < size {
		n := min(codexRolloutForwardChunk, size-pos)
		buf := make([]byte, n)
		if _, err := r.ReadAt(buf, pos); err != nil && !errors.Is(err, io.EOF) {
			return lineStart, err
		}
		pos += n
		data := append(carry, buf...)
		start := 0
		for {
			nl := bytes.IndexByte(data[start:], '\n')
			if nl < 0 {
				break
			}
			if line := data[start : start+nl]; len(bytes.TrimSpace(line)) > 0 && !visit(lineStart+int64(start), line) {
				return lineStart + int64(start+nl+1), nil
			}
			start += nl + 1
		}
		lineStart += int64(start)
		carry = append([]byte(nil), data[start:]...)
	}
	// A final record without its newline yet counts once it is whole JSON.
	if trailing := bytes.TrimSpace(carry); len(trailing) > 0 && json.Valid(trailing) {
		if !visit(lineStart, carry) {
			return size, nil
		}
		return size, nil
	}
	return lineStart, nil
}

// codexOffsetAtLineStart reports whether offset is a record boundary of r:
// the file start, just past a newline, or just past a whole final record
// that is still waiting for its newline (the scan's End in that case).
func codexOffsetAtLineStart(r io.ReaderAt, size, offset int64) bool {
	if offset == 0 {
		return true
	}
	if offset < 0 || offset > size {
		return false
	}
	var previous [1]byte
	if _, err := r.ReadAt(previous[:], offset-1); err != nil {
		return false
	}
	return previous[0] == '\n' || previous[0] == '}'
}

type codexRolloutTurnScan struct {
	TurnID      string
	StartOffset int64
	StartTime   time.Time
	Open        bool
	PromptSeen  bool
	End         int64
}

// scanCodexTurnState finds the newest turn start in [0, limit) and whether
// that turn is still open, reading back as far as the start lies.
func scanCodexTurnState(r io.ReaderAt, limit int64) (codexRolloutTurnScan, error) {
	var state codexRolloutTurnScan
	sawLifecycle, promptSeen, found := false, false, false
	end, err := scanCodexRolloutBackward(r, limit, func(offset int64, line []byte) bool {
		info := classifyCodexRolloutLine(line)
		switch info.Kind {
		case codexRecordUserInput:
			promptSeen = true
		case codexRecordComplete, codexRecordAborted:
			sawLifecycle = true
		case codexRecordStart:
			state.TurnID, state.StartOffset, state.StartTime = info.TurnID, offset, info.Time
			state.Open = !sawLifecycle
			state.PromptSeen = state.Open && promptSeen
			found = true
			return false
		}
		return true
	})
	if err != nil && !(found && errors.Is(err, errCodexRolloutScanCeiling)) {
		return codexRolloutTurnScan{}, err
	}
	state.End = end
	return state, nil
}

func scanCodexRolloutFile(path string) (codexRolloutTurnScan, error) {
	f, err := os.Open(path) // #nosec G304 -- callers resolve one exact, non-symlink rollout
	if err != nil {
		return codexRolloutTurnScan{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return codexRolloutTurnScan{}, err
	}
	return scanCodexTurnState(f, info.Size())
}

// codexOffsetAtTime returns the offset of the earliest line at or after
// notBefore, searching back from EOF until the records are well before it.
func codexOffsetAtTime(r io.ReaderAt, size int64, notBefore time.Time) (int64, error) {
	cut := int64(-1)
	stopBefore := notBefore.Add(-codexTimeCutLookback)
	end, err := scanCodexRolloutBackward(r, size, func(offset int64, line []byte) bool {
		stamp := codexLineTime(line)
		if stamp.IsZero() {
			return true
		}
		if !stamp.Before(notBefore) {
			cut = offset
			return true
		}
		return !stamp.Before(stopBefore)
	})
	if err != nil {
		return 0, err
	}
	if cut < 0 {
		return end, nil
	}
	return cut, nil
}

// codexDeliveryEvidenceIn searches the rollout at path for proof that an
// attempt whose cut is (cutOffset, notBefore) reached Codex: a turn start
// after the cut, or user input folded into a turn that was already running
// (never the running turn's own opening prompt, never injected context).
// cutOffset < 0 means "unknown" (a v1 marker): the cut is then derived from
// notBefore. Every candidate must also carry a timestamp no earlier than
// notBefore minus the clock slack, so an offset that does not describe this
// file (a replaced rollout) cannot attribute an old record.
// codexCutContext is the turn context at a cut: whether a turn was open and
// whether its opening prompt was already written.
type codexCutContext struct {
	open, promptSeen bool
}

func codexDeliveryEvidenceIn(path, sessionID string, cutOffset int64, notBefore time.Time) (*CodexDeliveryEvidence, error) {
	return codexDeliveryEvidenceWithContext(path, sessionID, cutOffset, notBefore, nil)
}

// codexDeliveryEvidenceWithContext is codexDeliveryEvidenceIn for a caller
// that may already know the turn context at cutOffset (the acceptance
// fence); known is ignored when the offset does not describe the file.
func codexDeliveryEvidenceWithContext(path, sessionID string, cutOffset int64, notBefore time.Time, known *codexCutContext) (*CodexDeliveryEvidence, error) {
	f, err := os.Open(path) // #nosec G304 -- callers resolve one exact, non-symlink rollout
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	floor := time.Time{}
	if !notBefore.IsZero() {
		floor = notBefore.Add(-codexEvidenceClockSlack)
	}
	basis := CodexEvidenceBasisOffset
	cut := cutOffset
	if cut < 0 || !codexOffsetAtLineStart(f, size, cut) {
		if floor.IsZero() {
			return nil, fmt.Errorf("no usable cut for Codex delivery evidence")
		}
		basis = CodexEvidenceBasisCreatedAt
		if cut, err = codexOffsetAtTime(f, size, floor); err != nil {
			return nil, err
		}
	}
	var open, promptSeen bool
	if known != nil && basis == CodexEvidenceBasisOffset {
		open, promptSeen = known.open, known.promptSeen
	} else {
		before, err := scanCodexTurnState(f, cut)
		if err != nil {
			return nil, err
		}
		open, promptSeen = before.Open, before.PromptSeen
	}
	var evidence *CodexDeliveryEvidence
	if _, err := scanCodexRolloutForward(f, size, cut, func(offset int64, line []byte) bool {
		record := classifyCodexRolloutLine(line)
		tooEarly := !floor.IsZero() && !record.Time.IsZero() && record.Time.Before(floor)
		switch record.Kind {
		case codexRecordStart:
			if tooEarly {
				open, promptSeen = true, false
				return true
			}
			evidence = &CodexDeliveryEvidence{Kind: CodexEvidenceNewTurn, Basis: basis, RecordOffset: offset,
				RecordTime: record.Time, TurnGeneration: sessionID + ":" + record.TurnID}
			return false
		case codexRecordComplete, codexRecordAborted:
			open, promptSeen = false, false
		case codexRecordUserInput:
			if open && !promptSeen {
				// The running turn's own opening prompt, written after its start.
				promptSeen = true
				return true
			}
			if tooEarly {
				return true
			}
			evidence = &CodexDeliveryEvidence{Kind: CodexEvidenceInputFolded, Basis: basis, RecordOffset: offset, RecordTime: record.Time}
			return false
		}
		return true
	}); err != nil {
		return nil, err
	}
	return evidence, nil
}

// CodexRolloutTurnState scans the exact rollout bound to this instance. A
// thread the pane's live Codex process owns but has not written a rollout
// for yet reports an empty state without error.
func (i *Instance) CodexRolloutTurnState() (CodexTurnState, error) {
	path, err := i.codexRolloutPath()
	if errors.Is(err, errNoExactContextArtifact) && i.LiveCodexThreadID() == i.CodexSessionID {
		return CodexTurnState{}, nil
	}
	if err != nil {
		return CodexTurnState{}, err
	}
	scan, err := scanCodexRolloutFile(path)
	if err != nil {
		return CodexTurnState{}, err
	}
	state := CodexTurnState{TurnOpen: scan.Open, PromptSeen: scan.PromptSeen, RolloutPresent: true, RolloutOffset: scan.End}
	if scan.TurnID != "" {
		state.Generation = i.CodexSessionID + ":" + scan.TurnID
	}
	return state, nil
}

// CodexDeliveryEvidenceSince reports proof in this instance's exact rollout
// that an attempt cut at (cutOffset, notBefore) reached Codex, or nil when
// there is none. A rollout that does not exist yet holds no evidence.
func (i *Instance) CodexDeliveryEvidenceSince(cutOffset int64, notBefore time.Time) (*CodexDeliveryEvidence, error) {
	path, err := i.codexRolloutPath()
	if errors.Is(err, errNoExactContextArtifact) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return codexDeliveryEvidenceIn(path, i.CodexSessionID, cutOffset, notBefore)
}

// CodexDeliveryEvidenceAfter is CodexDeliveryEvidenceSince for the sender
// that captured fence: the turn context at the fence is already known, so
// only the bytes written after it are read.
func (i *Instance) CodexDeliveryEvidenceAfter(fence CodexTurnState, notBefore time.Time) (*CodexDeliveryEvidence, error) {
	path, err := i.codexRolloutPath()
	if errors.Is(err, errNoExactContextArtifact) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return codexDeliveryEvidenceWithContext(path, i.CodexSessionID, fence.RolloutOffset, notBefore,
		&codexCutContext{open: fence.TurnOpen, promptSeen: fence.PromptSeen})
}

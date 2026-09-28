// Patch 21: tool_data.last_aida_ask — when Aida was last asked about this
// session (patch 19's `B`), and what the hub said happened to the ring.
//
// Written only for an ask that reached the doorbell: hub status accepted,
// held or routed. Refusals, cooldowns and every unconfirmed outcome leave the
// previous record (or none) in place.
//
// Stored as a JSON OBJECT, not a string:
//
//	{"at": <unix s>, "status": "accepted|held|routed", "ref": "<ring ref>",
//	 "channel": "<busProject/#busChannel>", "fallback": <bool>}
//
// The object is kept raw in memory and only the keys above are merged into
// it, so fields this binary does not know survive a load -> save round trip.
// Patch 24 adds "answered_at" (unix s), set when ask-aida.sh --status reports
// that Aida acked the ring. A NEW ask starts a fresh object on purpose: an
// "answered_at" belonging to the previous ask must not mark this one
// answered.
//
// Storage rides the same extras-zone mechanism as patch 12's last_prompt_at:
// the key is not in statedb's toolDataBlob, so MergeToolDataExtras carries it
// through legacy binaries' full saves, and legacy rows read as "no record".
package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

const toolDataLastAidaAskKey = "last_aida_ask"

// AidaAsk is one recorded ask. The exported fields are the known keys; raw
// holds the whole stored object so unknown keys are written back untouched.
type AidaAsk struct {
	At       time.Time
	Status   string
	Ref      string
	Channel  string
	Fallback bool
	// Patch 24: when the deck saw Aida answer the ring (zero = unanswered).
	AnsweredAt time.Time
	raw        json.RawMessage
}

// AidaAskReplyWindow is how long an unanswered ask counts as waiting for a
// reply (row marker, preview/dialog wording, status polling). Patch 24.
const AidaAskReplyWindow = 48 * time.Hour

// Answered returns when Aida answered, and false while unanswered. Patch 24.
func (a AidaAsk) Answered() (time.Time, bool) {
	return a.AnsweredAt, !a.AnsweredAt.IsZero()
}

// IsAnswered reports whether answered_at is set. Patch 24.
func (a AidaAsk) IsAnswered() bool { return !a.AnsweredAt.IsZero() }

// WithAnswered returns a copy marked answered at t; JSON() merges it into the
// stored object, keeping every other field. Patch 24.
func (a AidaAsk) WithAnswered(t time.Time) AidaAsk {
	a.AnsweredAt = time.Unix(t.Unix(), 0).UTC()
	return a
}

// AwaitingReply reports an ask that rang Aida (accepted or held — never
// routed), has a ref, is unanswered and is younger than AidaAskReplyWindow.
// Patch 24.
func (a AidaAsk) AwaitingReply(now time.Time) bool {
	if a.Status != "accepted" && a.Status != "held" {
		return false
	}
	if strings.TrimSpace(a.Ref) == "" || a.IsAnswered() || a.At.IsZero() {
		return false
	}
	return now.Sub(a.At) < AidaAskReplyWindow
}

// NewAidaAsk builds a fresh record (no inherited unknown fields).
func NewAidaAsk(at time.Time, status, ref, channel string, fallback bool) AidaAsk {
	return AidaAsk{At: at, Status: status, Ref: ref, Channel: channel, Fallback: fallback}
}

// JSON renders the record as an object: the stored raw object with the known
// keys merged over it.
func (a AidaAsk) JSON() json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(a.raw) > 0 {
		_ = json.Unmarshal(a.raw, &m)
	}
	put := func(k string, v any) {
		b, _ := json.Marshal(v)
		m[k] = b
	}
	put("at", a.At.Unix())
	put("status", a.Status)
	put("ref", a.Ref)
	put("channel", a.Channel)
	put("fallback", a.Fallback)
	// Patch 24: written only when set, so an unparseable stored value is
	// left exactly as it was.
	if !a.AnsweredAt.IsZero() {
		put("answered_at", a.AnsweredAt.Unix())
	}
	out, _ := json.Marshal(m)
	return out
}

// ParseAidaAsk reads a stored record. Anything that is not an object with a
// positive "at" is "no record" — never "asked at epoch".
func ParseAidaAsk(raw json.RawMessage) (AidaAsk, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return AidaAsk{}, false
	}
	var v struct {
		At       int64  `json:"at"`
		Status   string `json:"status"`
		Ref      string `json:"ref"`
		Channel  string `json:"channel"`
		Fallback bool   `json:"fallback"`
	}
	if err := json.Unmarshal(trimmed, &v); err != nil || v.At <= 0 {
		return AidaAsk{}, false
	}
	return AidaAsk{
		At:         time.Unix(v.At, 0).UTC(),
		Status:     strings.TrimSpace(v.Status),
		Ref:        v.Ref,
		Channel:    v.Channel,
		Fallback:   v.Fallback,
		AnsweredAt: parseAidaAnsweredAt(trimmed),
		raw:        append(json.RawMessage(nil), trimmed...),
	}, true
}

// parseAidaAnsweredAt reads answered_at on its own, so a malformed value
// means "unanswered" rather than failing the whole record. Only a positive
// integer counts. Patch 24.
func parseAidaAnsweredAt(obj json.RawMessage) time.Time {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(obj, &m); err != nil {
		return time.Time{}
	}
	raw, ok := m["answered_at"]
	if !ok {
		return time.Time{}
	}
	var sec int64
	if err := json.Unmarshal(raw, &sec); err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// ReadLastAidaAskFromToolData returns the stored object, or nil when the row
// has none (legacy, never asked, or not an object).
func ReadLastAidaAskFromToolData(td json.RawMessage) json.RawMessage {
	if len(td) == 0 {
		return nil
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(td, &m); err != nil {
		return nil
	}
	raw := bytes.TrimSpace(m[toolDataLastAidaAskKey])
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// WriteLastAidaAskToToolData merges the record into the tool_data blob. A nil
// record REMOVES the key rather than writing null or {}: MergeToolDataExtras
// lets an explicitly set key win, so a saver that has not seen a record must
// omit it, or it would wipe one another process just wrote.
func WriteLastAidaAskToToolData(td json.RawMessage, rec json.RawMessage) json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(td) > 0 {
		_ = json.Unmarshal(td, &m)
	}
	rec = bytes.TrimSpace(rec)
	if len(rec) > 0 && rec[0] == '{' {
		m[toolDataLastAidaAskKey] = rec
	} else {
		delete(m, toolDataLastAidaAskKey)
	}
	out, _ := json.Marshal(m)
	return out
}

// LastAidaAsk returns the last ask that reached Aida's doorbell. Thread-safe.
func (i *Instance) LastAidaAsk() (AidaAsk, bool) {
	i.mu.RLock()
	raw := i.lastAidaAsk
	i.mu.RUnlock()
	return ParseAidaAsk(raw)
}

// lastAidaAskRaw is the stored object for the save path, or nil.
func (i *Instance) lastAidaAskRaw() json.RawMessage {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.lastAidaAsk
}

// SetLastAidaAsk records a new ask in memory and returns the object to
// persist. It does no I/O: the caller hands the bytes to
// statedb.WriteLastAidaAsk off the UI goroutine.
func (i *Instance) SetLastAidaAsk(a AidaAsk) json.RawMessage {
	out := a.JSON()
	i.mu.Lock()
	i.lastAidaAsk = out
	i.mu.Unlock()
	return out
}

// MarkLastAidaAskAnswered sets answered_at = at on the stored record, but only
// when it is still the ask with this ref and not already answered: a new `B`
// ask that replaced the record while its predecessor's ring was being checked
// must stay unanswered. Returns the object to persist, and false when nothing
// changed. No I/O. Patch 24.
func (i *Instance) MarkLastAidaAskAnswered(ref string, at time.Time) (json.RawMessage, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	rec, ok := ParseAidaAsk(i.lastAidaAsk)
	if !ok || rec.Ref != ref || strings.TrimSpace(ref) == "" || rec.IsAnswered() {
		return nil, false
	}
	out := rec.WithAnswered(at).JSON()
	i.lastAidaAsk = out
	return out, true
}

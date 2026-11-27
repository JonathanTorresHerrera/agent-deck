// Package nativeprovider is an unattached, closed Codex provider protocol.
// It never invokes Deck's terminal, approval, StopWithOptions or queue paths.
package nativeprovider

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const Protocol = "ops-codex-native-v1"
const MaxPayload = 65536

var ErrClosed = errors.New("native_closed")
var ErrUncertain = errors.New("native_uncertain")

type Binding struct {
	PersonID string `json:"personId"`
	MachineID string `json:"machineId"`
	DeckSessionID string `json:"deckSessionId"`
}
type Authority struct {
	Epoch string `json:"epoch"`
	VerifiedAt string `json:"verifiedAt"`
	ExpiresAt string `json:"expiresAt"`
	CommandCreatedAt string `json:"commandCreatedAt"`
	CommandExpiresAt string `json:"commandExpiresAt"`
}
type Expected struct {
	Generation string `json:"generation,omitempty"`
	NativeThreadID string `json:"nativeThreadId"`
	FullPromptSHA256 string `json:"fullPromptSha256,omitempty"`
	LifecycleGeneration string `json:"lifecycleGeneration,omitempty"`
}
type Request struct {
	Protocol string `json:"protocol"`
	Operation string `json:"operation"`
	RequestKey string `json:"requestKey"`
	Binding Binding `json:"binding"`
	Incarnation string `json:"incarnation"`
	Epoch string `json:"epoch"`
	CommandID string `json:"commandId,omitempty"`
	DeliveryID string `json:"deliveryId,omitempty"`
	Authority *Authority `json:"authority,omitempty"`
	Expected *Expected `json:"expected,omitempty"`
	OptionOrdinal *int `json:"optionOrdinal,omitempty"`
	Confirmed *bool `json:"confirmed,omitempty"`
	RequestSHA256 string `json:"requestSha256,omitempty"`
}
type Source struct {
	NativeRevision string `json:"nativeRevision"`
	NativePatchSHA256 string `json:"nativePatchSha256"`
}
type QuestionOption struct { Label string `json:"label"`; Description string `json:"description"` }
type Question struct {
	ID string `json:"id"`; Header string `json:"header"`; Question string `json:"question"`
	IsOther bool `json:"isOther"`; IsSecret bool `json:"isSecret"`
	Options *[]QuestionOption `json:"options,omitempty"`
}
type QuestionEvent struct {
	CallID string `json:"call_id"`; TurnID string `json:"turn_id"`; Questions []Question `json:"questions"`
	IsBlocking bool `json:"isBlocking"`; AutoResolutionMS *uint64 `json:"autoResolutionMs,omitempty"`
}
type Observation struct {
	Origin string `json:"origin"`; Incarnation string `json:"incarnation"`; Generation string `json:"generation"`
	NativeThreadID string `json:"nativeThreadId"`; FullPromptSHA256 string `json:"fullPromptSha256"`
	Source; Event QuestionEvent `json:"event"`
}
type Lifecycle struct { NativeThreadID string `json:"nativeThreadId"`; Generation string `json:"generation"`; State string `json:"state"` }
type Receipt struct {
	RequestSHA256 string `json:"requestSha256"`; CommandID string `json:"commandId"`; DeliveryID string `json:"deliveryId"`
	RequestKey string `json:"requestKey"`; Binding Binding `json:"binding"`; Incarnation string `json:"incarnation"`; Epoch string `json:"epoch"`
	Operation string `json:"operation"`; NativeThreadID string `json:"nativeThreadId"`; Generation string `json:"generation"`
	FullPromptSHA256 string `json:"fullPromptSha256,omitempty"`; OptionOrdinal *int `json:"optionOrdinal,omitempty"`
	Outcome string `json:"outcome"`; Entered bool `json:"entered"`; Code string `json:"code"`
}
type Response struct {
	Protocol string `json:"protocol"`; Operation string `json:"operation"`; RequestKey string `json:"requestKey"`
	Binding Binding `json:"binding"`; Incarnation string `json:"incarnation"`; Epoch string `json:"epoch"`
	Status string `json:"status"`; Source
	Prompt *Observation `json:"prompt"`; Lifecycle *Lifecycle `json:"lifecycle"`
	Receipt *Receipt `json:"receipt"`; Code string `json:"code"`
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var hash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var revision = regexp.MustCompile(`^[0-9a-f]{40}$`)
var codes = map[string]bool{"accepted":true,"closed":true,"binding_mismatch":true,"authority_expired":true,"authority_revoked":true,"stale_generation":true,"prompt_mismatch":true,"protected":true,"unsupported":true,"busy":true,"resources":true,"already_pending":true,"receiver_closed":true,"entry_failed":true,"receipt_missing":true,"receipt_conflict":true,"capacity":true}
const commonKeys = "protocol operation requestKey binding incarnation epoch"

func validBinding(b Binding) bool { return identifier.MatchString(b.PersonID) && identifier.MatchString(b.MachineID) && identifier.MatchString(b.DeckSessionID) }
func validSource(s Source) bool { return revision.MatchString(s.NativeRevision) && hash.MatchString(s.NativePatchSHA256) }
func object(raw []byte, required, optional string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw,&m)!=nil || m==nil { return nil,ErrClosed }
	allowed:=map[string]bool{}
	for _,k:=range strings.Fields(required) { allowed[k]=true; if _,ok:=m[k]; !ok { return nil,ErrClosed } }
	for _,k:=range strings.Fields(optional) { allowed[k]=true }
	for k:=range m { if !allowed[k] { return nil,ErrClosed } }
	return m,nil
}
func canonical(v any) ([]byte,error) {
	var b bytes.Buffer; e:=json.NewEncoder(&b); e.SetEscapeHTML(false)
	if e.Encode(v)!=nil { return nil,ErrClosed }
	raw:=bytes.TrimSuffix(b.Bytes(),[]byte{'\n'})
	// encoding/json always escapes these two runes; Rust/JS canonical UTF-8 does
	// not. Skip escaped backslashes, so a literal "\\u2028" stays literal.
	var out []byte
	for i:=0;i<len(raw);i++ { if raw[i]=='\\' && i+1<len(raw) {
		if i+5<len(raw) && (string(raw[i:i+6])==`\u2028` || string(raw[i:i+6])==`\u2029`) { if raw[i+5]=='8' { out=append(out,[]byte("\u2028")...) } else { out=append(out,[]byte("\u2029")...) }; i+=5; continue }
		out=append(out,raw[i],raw[i+1]); i++; continue
	}; out=append(out,raw[i]) }
	if len(out)==0 || len(out)>MaxPayload { return nil,ErrClosed }; return out,nil
}

// Reject invalid UTF-8 and unpaired escaped surrogates before Go can replace
// them, then reject duplicate decoded keys at every bounded nesting depth.
func strict(raw []byte) error {
	if len(raw)==0 || len(raw)>MaxPayload || !utf8.Valid(raw) || !json.Valid(raw) { return ErrClosed }
	in:=false
	for i:=0;i<len(raw);i++ { if raw[i]=='"' { in=!in; continue }; if !in || raw[i]!='\\' { continue }; i++; if raw[i]!='u' { continue }
		v:=raw[i+1:i+5]; var n uint16
		for _,c:=range v { n<<=4; switch { case c>='0'&&c<='9':n+=uint16(c-'0'); case c>='a'&&c<='f':n+=uint16(c-'a'+10); default:n+=uint16(c-'A'+10) } }; i+=4
		if n>=0xDC00 && n<=0xDFFF { return ErrClosed }
		if n>=0xD800 && n<=0xDBFF { if i+6>=len(raw)||raw[i+1]!='\\'||raw[i+2]!='u' { return ErrClosed }; next:=raw[i+3:i+7]; var low uint16
			for _,c:=range next { low<<=4; switch { case c>='0'&&c<='9':low+=uint16(c-'0'); case c>='a'&&c<='f':low+=uint16(c-'a'+10); default:low+=uint16(c-'A'+10) } }; if low<0xDC00||low>0xDFFF { return ErrClosed }; i+=6 }
	}
	d:=json.NewDecoder(bytes.NewReader(raw)); d.UseNumber()
	var walk func(int) error
	walk=func(depth int) error { if depth>16 { return ErrClosed }; token,err:=d.Token(); if err!=nil { return ErrClosed }; delimiter,ok:=token.(json.Delim); if !ok { return nil }
		switch delimiter { case '{': seen:=map[string]bool{}; for d.More() { key,e:=d.Token(); s,ok:=key.(string); if e!=nil||!ok||seen[s] { return ErrClosed }; seen[s]=true; if walk(depth+1)!=nil { return ErrClosed } }; _,err=d.Token()
		case '[': for d.More() { if walk(depth+1)!=nil { return ErrClosed } }; _,err=d.Token()
		default:return ErrClosed }; if err!=nil { return ErrClosed }; return nil }
	if walk(0)!=nil { return ErrClosed }; if _,err:=d.Token(); err!=io.EOF { return ErrClosed }; return nil
}
func parseTime(s string) (time.Time,bool) { t,e:=time.Parse("2006-01-02T15:04:05.000Z",s); return t,e==nil&&t.Format("2006-01-02T15:04:05.000Z")==s }
func authorityValid(a Authority) bool { v,ok1:=parseTime(a.VerifiedAt); e,ok2:=parseTime(a.ExpiresAt); c,ok3:=parseTime(a.CommandCreatedAt); d,ok4:=parseTime(a.CommandExpiresAt)
	return uuid.MatchString(a.Epoch)&&ok1&&ok2&&ok3&&ok4&&!c.Before(v)&&c.Before(e)&&e.After(v)&&e.Sub(v)<=15*time.Minute&&d.After(c)&&d.Sub(c)<=4*time.Hour }
func AuthorityCurrent(a Authority,now time.Time) bool { if !authorityValid(a) { return false }; v,_:=parseTime(a.VerifiedAt); e,_:=parseTime(a.ExpiresAt); c,_:=parseTime(a.CommandCreatedAt); d,_:=parseTime(a.CommandExpiresAt); return !now.Before(v)&&!now.Before(c)&&now.Before(e)&&now.Before(d) }

func DecodeRequest(raw []byte) (Request,error) {
	var r Request
	if strict(raw)!=nil || json.Unmarshal(raw,&r)!=nil { return r,ErrClosed }
	extra:=""
	switch r.Operation { case "observe","revoke": case "answer":extra="commandId deliveryId authority expected optionOrdinal"; case "stop":extra="commandId deliveryId authority expected confirmed"; case "receipt":extra="commandId deliveryId requestSha256"; default:return r,ErrClosed }
	m,e:=object(raw,commonKeys+" "+extra,""); if e!=nil || r.Protocol!=Protocol || !uuid.MatchString(r.RequestKey)||!validBinding(r.Binding)||!uuid.MatchString(r.Incarnation)||!uuid.MatchString(r.Epoch) { return r,ErrClosed }
	if _,e=object(m["binding"],"personId machineId deckSessionId","");e!=nil { return r,ErrClosed }
	if r.Operation=="answer"||r.Operation=="stop"||r.Operation=="receipt" { if !uuid.MatchString(r.CommandID)||!uuid.MatchString(r.DeliveryID) { return r,ErrClosed } }
	if r.Operation=="receipt" && !hash.MatchString(r.RequestSHA256) { return r,ErrClosed }
	if r.Operation=="answer"||r.Operation=="stop" {
		if r.Authority==nil||!authorityValid(*r.Authority)||r.Authority.Epoch!=r.Epoch||r.Expected==nil||!uuid.MatchString(r.Expected.NativeThreadID) { return r,ErrClosed }
		if _,e=object(m["authority"],"epoch verifiedAt expiresAt commandCreatedAt commandExpiresAt","");e!=nil { return r,ErrClosed }
		if r.Operation=="answer" { if _,e=object(m["expected"],"generation nativeThreadId fullPromptSha256","");e!=nil||!uuid.MatchString(r.Expected.Generation)||!hash.MatchString(r.Expected.FullPromptSHA256)||r.OptionOrdinal==nil||*r.OptionOrdinal<1||*r.OptionOrdinal>10 { return r,ErrClosed }
		} else { if _,e=object(m["expected"],"nativeThreadId lifecycleGeneration","");e!=nil||!uuid.MatchString(r.Expected.LifecycleGeneration)||r.Confirmed==nil||!*r.Confirmed { return r,ErrClosed } }
	}; return r,nil
}
func EncodeRequest(r Request) ([]byte,error) { raw,e:=canonical(r); if e!=nil { return nil,e }; if _,e=DecodeRequest(raw);e!=nil { return nil,e }; return raw,nil }
func ActionCommitment(r Request) (string,error) { if r.Operation!="answer"&&r.Operation!="stop" { return "",ErrClosed }; raw,e:=EncodeRequest(r); if e!=nil { return "",e }; sum:=sha256.Sum256(raw); return hex.EncodeToString(sum[:]),nil }
func Frame(raw []byte) ([]byte,error) { if strict(raw)!=nil { return nil,ErrClosed }; out:=make([]byte,4+len(raw)); binary.BigEndian.PutUint32(out,uint32(len(raw))); copy(out[4:],raw); return out,nil }
func Unframe(frame []byte) ([]byte,error) { if len(frame)<5||len(frame)>MaxPayload+4||int(binary.BigEndian.Uint32(frame))!=len(frame)-4||strict(frame[4:])!=nil { return nil,ErrClosed }; return frame[4:],nil }

func eventValid(raw []byte,ev QuestionEvent) bool {
	m,e:=object(raw,"call_id turn_id questions isBlocking","autoResolutionMs"); if e!=nil||len(ev.Questions)<1||len(ev.Questions)>100 { return false }
	for _,k:=range []string{"call_id","turn_id"} { if len(m[k])<2||m[k][0]!='"' { return false } }
	if string(m["isBlocking"])!="true"&&string(m["isBlocking"])!="false" { return false }
	if a,ok:=m["autoResolutionMs"];ok && (bytes.Equal(a,[]byte("null"))||ev.AutoResolutionMS==nil||*ev.AutoResolutionMS>9007199254740991) { return false }
	var qs []json.RawMessage; if json.Unmarshal(m["questions"],&qs)!=nil { return false }
	for i,q:=range qs { qm,err:=object(q,"id header question isOther isSecret","options"); if err!=nil { return false }; for _,k:=range []string{"id","header","question"} { if len(qm[k])<2||qm[k][0]!='"' { return false } }; for _,k:=range []string{"isOther","isSecret"} { if string(qm[k])!="true"&&string(qm[k])!="false" { return false } }; if o,ok:=qm["options"];ok { var opts []json.RawMessage; if json.Unmarshal(o,&opts)!=nil||opts==nil||len(opts)>100||ev.Questions[i].Options==nil { return false }; for _,opt:=range opts { om,err:=object(opt,"label description","");if err!=nil { return false };for _,k:=range []string{"label","description"} { if len(om[k])<2||om[k][0]!='"' { return false } } } } }
	return true
}
func PromptCommitment(incarnation,generation,thread string,event QuestionEvent) (string,error) {
	if !uuid.MatchString(incarnation)||!uuid.MatchString(generation)||!uuid.MatchString(thread) { return "",ErrClosed }
	if !utf8.ValidString(event.CallID)||!utf8.ValidString(event.TurnID) { return "",ErrClosed }; for _,q:=range event.Questions { if !utf8.ValidString(q.ID)||!utf8.ValidString(q.Header)||!utf8.ValidString(q.Question) { return "",ErrClosed }; if q.Options!=nil { for _,o:=range *q.Options { if !utf8.ValidString(o.Label)||!utf8.ValidString(o.Description) { return "",ErrClosed } } } }
	raw,e:=canonical(event); if e!=nil||!eventValid(raw,event) { return "",ErrClosed }
	tuple,e:=canonical([]any{Protocol,incarnation,generation,thread,event}); if e!=nil { return "",e }; sum:=sha256.Sum256(tuple); return hex.EncodeToString(sum[:]),nil
}

func DecodeResponse(raw []byte,request Request,source Source,original *Request) (Response,error) {
	var r Response; if _,e:=EncodeRequest(request);e!=nil { return r,ErrClosed };if strict(raw)!=nil||json.Unmarshal(raw,&r)!=nil { return r,ErrClosed }
	extra:=""; switch r.Status { case "observed":extra="nativeRevision nativePatchSha256 prompt lifecycle"; case "receipt":extra="receipt"; case "revoked":case "refused":extra="code"; default:return r,ErrClosed }
	m,e:=object(raw,commonKeys+" status "+extra,""); if e!=nil||r.Protocol!=request.Protocol||r.Operation!=request.Operation||r.RequestKey!=request.RequestKey||r.Binding!=request.Binding||r.Incarnation!=request.Incarnation||r.Epoch!=request.Epoch { return r,ErrClosed }
	if _,e=object(m["binding"],"personId machineId deckSessionId","");e!=nil { return r,ErrClosed }
	switch r.Status {
	case "refused":if !codes[r.Code]||r.Code=="accepted" { return r,ErrClosed }; return r,nil
	case "revoked":if request.Operation!="revoke" { return r,ErrClosed }; return r,nil
	case "observed":
		if request.Operation!="observe"||!validSource(source)||r.Source!=source||r.Lifecycle==nil||!uuid.MatchString(r.Lifecycle.NativeThreadID)||!uuid.MatchString(r.Lifecycle.Generation)||(r.Lifecycle.State!="safe_idle"&&r.Lifecycle.State!="busy"&&r.Lifecycle.State!="unsupported") { return r,ErrClosed }
		if _,e=object(m["lifecycle"],"nativeThreadId generation state","");e!=nil { return r,ErrClosed }
		if r.Prompt!=nil { p:=r.Prompt; pm,err:=object(m["prompt"],"origin incarnation generation nativeThreadId fullPromptSha256 nativeRevision nativePatchSha256 event",""); if err!=nil||p.Origin!="ordinary_question"||p.Source!=source||p.Incarnation!=r.Incarnation||p.NativeThreadID!=r.Lifecycle.NativeThreadID||!eventValid(pm["event"],p.Event) { return r,ErrClosed }; digest,err:=PromptCommitment(p.Incarnation,p.Generation,p.NativeThreadID,p.Event); if err!=nil||digest!=p.FullPromptSHA256 { return r,ErrClosed } }; return r,nil
	case "receipt":
		a:=request; if request.Operation=="receipt" { if original==nil { return r,ErrClosed }; a=*original } else if request.Operation!="answer"&&request.Operation!="stop" { return r,ErrClosed }
		digest,err:=ActionCommitment(a); if err!=nil||r.Receipt==nil { return r,ErrClosed }; p:=r.Receipt
		keys:="requestSha256 commandId deliveryId requestKey binding incarnation epoch operation nativeThreadId generation outcome entered code"; if p.Operation=="answer" { keys+=" fullPromptSha256 optionOrdinal" } else if p.Operation!="stop" { return r,ErrClosed }
		pm,err:=object(m["receipt"],keys,""); if err!=nil { return r,ErrClosed }; if string(pm["entered"])!="true"&&string(pm["entered"])!="false" { return r,ErrClosed }; if _,err=object(pm["binding"],"personId machineId deckSessionId","");err!=nil { return r,ErrClosed }
		if request.Operation=="receipt"&&(request.CommandID!=a.CommandID||request.DeliveryID!=a.DeliveryID||request.RequestKey!=a.RequestKey||request.RequestSHA256!=digest) { return r,ErrClosed }
		if a.Binding!=request.Binding||a.Incarnation!=request.Incarnation||a.Epoch!=request.Epoch||p.RequestSHA256!=digest||p.CommandID!=a.CommandID||p.DeliveryID!=a.DeliveryID||p.RequestKey!=a.RequestKey||p.Binding!=a.Binding||p.Incarnation!=a.Incarnation||p.Epoch!=a.Epoch||p.Operation!=a.Operation||p.NativeThreadID!=a.Expected.NativeThreadID||!codes[p.Code] { return r,ErrClosed }
		if a.Operation=="answer" { if p.Generation!=a.Expected.Generation||p.FullPromptSHA256!=a.Expected.FullPromptSHA256||p.OptionOrdinal==nil||*p.OptionOrdinal!=*a.OptionOrdinal { return r,ErrClosed } } else if p.Generation!=a.Expected.LifecycleGeneration { return r,ErrClosed }
		if p.Outcome=="applied" { if !p.Entered||p.Code!="accepted" { return r,ErrClosed } } else if p.Outcome=="refused" { if p.Entered||p.Code=="accepted" { return r,ErrClosed } } else if p.Outcome=="indeterminate" { if !p.Entered||p.Code=="accepted" { return r,ErrClosed } } else { return r,ErrClosed }; return r,nil
	}; return r,ErrClosed
}

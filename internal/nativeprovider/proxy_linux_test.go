//go:build linux

package nativeprovider

// Only isolated temporary Unix endpoints and synthetic records are used. No
// Deck registry, terminal, shell, model, credentials, real session or default
// runtime socket is discovered or called.
import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const incarnation = "11111111-1111-4111-8111-111111111111"
const generation = "22222222-2222-4222-8222-222222222222"
const thread = "33333333-3333-4333-8333-333333333333"
const epoch = "44444444-4444-4444-8444-444444444444"
const requestKey = "55555555-5555-4555-8555-555555555555"
const commandID = "66666666-6666-4666-8666-666666666666"
const deliveryID = "77777777-7777-4777-8777-777777777777"

var fixtureBinding = Binding{"synthetic-owner", "synthetic-machine", "synthetic-session"}
var fixtureSource = Source{strings.Repeat("c", 40), strings.Repeat("d", 64)}

func questionFixture(t *testing.T) Observation {
	t.Helper()
	raw, e := os.ReadFile("testdata/prompt-vector.json")
	if e != nil {
		t.Fatal(e)
	}
	var value struct {
		Incarnation      string        `json:"incarnation"`
		Generation       string        `json:"generation"`
		NativeThreadID   string        `json:"nativeThreadId"`
		Event            QuestionEvent `json:"event"`
		FullPromptSHA256 string        `json:"fullPromptSha256"`
	}
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("fixture malformed")
	}
	return Observation{"ordinary_question", value.Incarnation, value.Generation, value.NativeThreadID, value.FullPromptSHA256, fixtureSource, value.Event}
}
func base(operation string) Request {
	return Request{Protocol: Protocol, Operation: operation, RequestKey: requestKey, Binding: fixtureBinding, Incarnation: incarnation, Epoch: epoch}
}
func action(t *testing.T) Request {
	t.Helper()
	r := base("answer")
	r.CommandID = commandID
	r.DeliveryID = deliveryID
	now := time.Now().UTC()
	stamp := func(t time.Time) string { return t.Format("2006-01-02T15:04:05.000Z") }
	r.Authority = &Authority{epoch, stamp(now.Add(-time.Second)), stamp(now.Add(14 * time.Minute)), stamp(now.Add(-time.Second)), stamp(now.Add(time.Hour))}
	r.Expected = &Expected{Generation: generation, NativeThreadID: thread, FullPromptSHA256: questionFixture(t).FullPromptSHA256}
	n := 1
	r.OptionOrdinal = &n
	return r
}
func envelope(r Request) map[string]any {
	return map[string]any{"protocol": r.Protocol, "operation": r.Operation, "requestKey": r.RequestKey, "binding": r.Binding, "incarnation": r.Incarnation, "epoch": r.Epoch}
}
func receiptObject(t *testing.T, r Request) map[string]any {
	t.Helper()
	hash, e := ActionCommitment(r)
	if e != nil {
		t.Fatal(e)
	}
	p := map[string]any{"requestSha256": hash, "commandId": r.CommandID, "deliveryId": r.DeliveryID, "requestKey": r.RequestKey, "binding": r.Binding, "incarnation": r.Incarnation, "epoch": r.Epoch, "operation": r.Operation, "nativeThreadId": r.Expected.NativeThreadID, "generation": r.Expected.Generation, "outcome": "applied", "entered": true, "code": "accepted"}
	if r.Operation == "answer" {
		p["fullPromptSha256"] = r.Expected.FullPromptSHA256
		p["optionOrdinal"] = *r.OptionOrdinal
	} else {
		p["generation"] = r.Expected.LifecycleGeneration
	}
	return p
}
func marshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

type fakeEndpoint struct {
	proxy       *Proxy
	options     Options
	calls       atomic.Int32
	wrongSource atomic.Bool
	listen      *net.UnixListener
	directory   string
}

func endpoint(t *testing.T, handler func(Request) []byte) *fakeEndpoint {
	t.Helper()
	root, e := os.MkdirTemp("", "ops-native-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	name := filepath.Join(root, "provider.sock")
	ln, e := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if os.Chmod(name, 0600) != nil {
		t.Fatal("chmod")
	}
	dirInfo, _ := os.Lstat(root)
	socketInfo, _ := os.Lstat(name)
	di, _ := statIdentity(dirInfo)
	si, _ := statIdentity(socketInfo)
	opts := Options{name, di, si, uint32(os.Geteuid()), int32(os.Getpid()), fixtureBinding, incarnation, epoch, fixtureSource, time.Second}
	proxy, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	f := &fakeEndpoint{proxy: proxy, options: opts, listen: ln, directory: root}
	go func() {
		for {
			conn, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				raw, err := io.ReadAll(io.LimitReader(conn, MaxPayload+5))
				if err != nil {
					return
				}
				body, err := Unframe(raw)
				if err != nil {
					return
				}
				r, err := DecodeRequest(body)
				if err != nil {
					return
				}
				f.calls.Add(1)
				var reply []byte
				if r.Operation == "observe" {
					m := envelope(r)
					m["status"] = "observed"
					m["nativeRevision"] = fixtureSource.NativeRevision
					m["nativePatchSha256"] = fixtureSource.NativePatchSHA256
					p := questionFixture(t)
					if f.wrongSource.Load() {
						m["nativePatchSha256"] = strings.Repeat("e", 64)
						p.NativePatchSHA256 = strings.Repeat("e", 64)
					}
					m["prompt"] = p
					m["lifecycle"] = Lifecycle{thread, generation, "busy"}
					reply = framed(t, m)
				} else {
					reply = handler(r)
				}
				_, _ = conn.Write(reply)
			}()
		}
	}()
	return f
}
func qualify(t *testing.T, f *fakeEndpoint) {
	t.Helper()
	r, e := f.proxy.Exchange(context.Background(), base("observe"), nil)
	if e != nil || r.Status != "observed" || !f.proxy.sourceReady() {
		t.Fatal("source handshake failed")
	}
}
func framed(t *testing.T, value any) []byte {
	t.Helper()
	raw := marshal(t, value)
	f, e := Frame(raw)
	if e != nil {
		t.Fatal(e)
	}
	return f
}

func TestNativeProvider(t *testing.T) {
	t.Run("later failed source observation invalidates earlier handshake", func(t *testing.T) {
		f := endpoint(t, func(r Request) []byte { return nil })
		qualify(t, f)
		f.wrongSource.Store(true)
		if _, e := f.proxy.Exchange(context.Background(), base("observe"), nil); !errors.Is(e, ErrClosed) || f.proxy.sourceReady() {
			t.Fatal("failed observation kept prior source")
		}
		before := f.calls.Load()
		if _, e := f.proxy.Exchange(context.Background(), action(t), nil); !errors.Is(e, ErrClosed) || f.calls.Load() != before {
			t.Fatal("action escaped after source mismatch")
		}
	})
	t.Run("no action bytes before exact pinned observation", func(t *testing.T) {
		a := action(t)
		f := endpoint(t, func(r Request) []byte {
			m := envelope(r)
			m["status"] = "receipt"
			m["receipt"] = receiptObject(t, a)
			return framed(t, m)
		})
		if _, e := f.proxy.Exchange(context.Background(), a, nil); !errors.Is(e, ErrClosed) || f.calls.Load() != 0 {
			t.Fatal("action escaped before source handshake")
		}
		qualify(t, f)
		if _, e := f.proxy.Exchange(context.Background(), a, nil); e != nil || f.calls.Load() != 2 {
			t.Fatal("qualified action failed")
		}
		opts := f.options
		opts.Source.NativePatchSHA256 = strings.Repeat("e", 64)
		p, e := New(opts)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.Exchange(context.Background(), base("observe"), nil); !errors.Is(e, ErrClosed) || p.sourceReady() {
			t.Fatal("wrong patch qualified")
		}
		before := f.calls.Load()
		if _, e = p.Exchange(context.Background(), a, nil); !errors.Is(e, ErrClosed) || f.calls.Load() != before {
			t.Fatal("action escaped wrong patch")
		}
	})
	t.Run("shared full prompt vector and hidden native fields", func(t *testing.T) {
		p := questionFixture(t)
		sum, e := PromptCommitment(p.Incarnation, p.Generation, p.NativeThreadID, p.Event)
		if e != nil || sum != p.FullPromptSHA256 || sum != "66895da3047e59c08c12f128658a56e95a18e00086d613dbe2a24755a9c6a112" {
			t.Fatal("cross-language vector mismatch")
		}
		p.Event.Questions[0].Header += " "
		changed, _ := PromptCommitment(p.Incarnation, p.Generation, p.NativeThreadID, p.Event)
		if changed == sum {
			t.Fatal("full field omitted")
		}
	})
	t.Run("shared original request commitment", func(t *testing.T) {
		raw, e := os.ReadFile("testdata/request-vector.json")
		if e != nil {
			t.Fatal(e)
		}
		var vector struct {
			Request       json.RawMessage `json:"request"`
			RequestSHA256 string          `json:"requestSha256"`
		}
		if json.Unmarshal(raw, &vector) != nil {
			t.Fatal("fixture")
		}
		request, e := DecodeRequest(vector.Request)
		if e != nil {
			t.Fatal(e)
		}
		digest, e := ActionCommitment(request)
		if e != nil || digest != vector.RequestSHA256 || digest != "ce32273ab5cfeb421280a9be38896d3ee3f153886258710062192853702491ba" {
			t.Fatal("request canonical mismatch")
		}
	})
	t.Run("shared Unicode separator commitment", func(t *testing.T) {
		raw, e := os.ReadFile("testdata/unicode-vector.json")
		if e != nil {
			t.Fatal(e)
		}
		var vector struct {
			Incarnation      string        `json:"incarnation"`
			Generation       string        `json:"generation"`
			NativeThreadID   string        `json:"nativeThreadId"`
			Event            QuestionEvent `json:"event"`
			FullPromptSHA256 string        `json:"fullPromptSha256"`
		}
		if json.Unmarshal(raw, &vector) != nil {
			t.Fatal("fixture")
		}
		digest, e := PromptCommitment(vector.Incarnation, vector.Generation, vector.NativeThreadID, vector.Event)
		if e != nil || digest != vector.FullPromptSHA256 || digest != "7a7fbc1e78187b862d10718e95ae7836684f6a5c8f11e4c831b91f4d72ee36d6" {
			t.Fatal("Unicode canonical mismatch")
		}
	})
	t.Run("Unicode separators and literal escape remain distinct", func(t *testing.T) {
		p := questionFixture(t)
		p.Event.CallID = "literal \\u2028 and actual \u2028 plus <&>"
		raw, e := canonical(p.Event)
		if e != nil || !strings.Contains(string(raw), `literal \\u2028 and actual `) || !strings.Contains(string(raw), "\u2028 plus <&>") {
			t.Fatal("canonical Unicode changed")
		}
		if strict(raw) != nil {
			t.Fatal("canonical JSON invalid")
		}
	})
	t.Run("closed request parser rejects duplicates aliases null unknown and malformed encodings", func(t *testing.T) {
		r := action(t)
		raw, e := EncodeRequest(r)
		if e != nil {
			t.Fatal(e)
		}
		cases := [][]byte{[]byte(`{"protocol":"ops-codex-native-v1","protocol":"x"}`), []byte(strings.Replace(string(raw), `"personId":"synthetic-owner"`, `"personId":"other","person\u0049d":"synthetic-owner"`, 1)), []byte(strings.Replace(string(raw), `"optionOrdinal":1`, `"optionOrdinal":null`, 1)), []byte(strings.Replace(string(raw), `"operation":"answer"`, `"operation":"trust"`, 1)), []byte(strings.Replace(string(raw), `"expected":`, `"extra":"CANARY","expected":`, 1)), []byte(`{"x":"\ud800"}`), []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}}
		for _, body := range cases {
			if _, e := DecodeRequest(body); !errors.Is(e, ErrClosed) {
				t.Fatal("unsafe request accepted")
			}
		}
	})
	t.Run("later stepup cannot renew earlier command before first native admission", func(t *testing.T) {
		r := action(t)
		a := *r.Authority
		created, _ := parseTime(a.CommandCreatedAt)
		a.VerifiedAt = created.Add(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
		r.Authority = &a
		if authorityValid(a) {
			t.Fatal("later approval renewed original command")
		}
		if _, e := DecodeRequest(marshal(t, r)); !errors.Is(e, ErrClosed) {
			t.Fatal("renewed first request admitted")
		}
		a = *action(t).Authority
		a.CommandCreatedAt = a.ExpiresAt
		if authorityValid(a) {
			t.Fatal("creation at approval endpoint admitted")
		}
		a = *action(t).Authority
		a.VerifiedAt = a.CommandCreatedAt
		if !authorityValid(a) {
			t.Fatal("equal original approval and creation refused")
		}
	})
	t.Run("native approval and command deadlines are exclusive", func(t *testing.T) {
		r := action(t)
		now := time.Now()
		if !AuthorityCurrent(*r.Authority, now) {
			t.Fatal("valid deadline refused")
		}
		until, _ := parseTime(r.Authority.ExpiresAt)
		if AuthorityCurrent(*r.Authority, until) {
			t.Fatal("expiry inclusive")
		}
		a := *r.Authority
		a.ExpiresAt = until.Add(time.Minute + time.Second).Format("2006-01-02T15:04:05.000Z")
		if authorityValid(a) {
			t.Fatal("approval extended")
		}
	})
	t.Run("actual private exchange preserves full prompt and both source pins", func(t *testing.T) {
		p := questionFixture(t)
		f := endpoint(t, func(r Request) []byte {
			m := envelope(r)
			m["status"] = "observed"
			m["nativeRevision"] = fixtureSource.NativeRevision
			m["nativePatchSha256"] = fixtureSource.NativePatchSHA256
			m["prompt"] = p
			m["lifecycle"] = Lifecycle{thread, generation, "busy"}
			return framed(t, m)
		})
		r, e := f.proxy.Exchange(context.Background(), base("observe"), nil)
		if e != nil || r.Prompt == nil || r.Prompt.FullPromptSHA256 != p.FullPromptSHA256 || f.calls.Load() != 1 {
			t.Fatal("observation failed")
		}
		if r.Source != fixtureSource {
			t.Fatal("source drift")
		}
	})
	t.Run("receipt correlation and read-only lookup do not create a second effect", func(t *testing.T) {
		a := action(t)
		saved := receiptObject(t, a)
		var effects atomic.Int32
		f := endpoint(t, func(r Request) []byte {
			m := envelope(r)
			m["status"] = "receipt"
			m["receipt"] = saved
			if r.Operation == "answer" {
				effects.Add(1)
			}
			return framed(t, m)
		})
		qualify(t, f)
		first, e := f.proxy.Exchange(context.Background(), a, nil)
		if e != nil {
			t.Fatal(e)
		}
		lookup := base("receipt")
		lookup.CommandID = a.CommandID
		lookup.DeliveryID = a.DeliveryID
		lookup.RequestSHA256, _ = ActionCommitment(a)
		second, e := f.proxy.Exchange(context.Background(), lookup, &a)
		if e != nil || second.Receipt.RequestSHA256 != first.Receipt.RequestSHA256 || effects.Load() != 1 {
			t.Fatal("receipt recovery entered")
		}
		if _, e = f.proxy.Exchange(context.Background(), lookup, nil); !errors.Is(e, ErrClosed) {
			t.Fatal("lookup without original was accepted")
		}
	})
	t.Run("acknowledged revoke latches proxy before later mutations", func(t *testing.T) {
		var revoked atomic.Bool
		var effects atomic.Int32
		f := endpoint(t, func(r Request) []byte {
			m := envelope(r)
			if r.Operation == "revoke" {
				revoked.Store(true)
				m["status"] = "revoked"
			} else {
				if !revoked.Load() {
					effects.Add(1)
				}
				m["status"] = "refused"
				m["code"] = "authority_revoked"
			}
			return framed(t, m)
		})
		qualify(t, f)
		r, e := f.proxy.Exchange(context.Background(), base("revoke"), nil)
		if e != nil || r.Status != "revoked" {
			t.Fatal("revoke not acknowledged")
		}
		if _, e = f.proxy.Exchange(context.Background(), action(t), nil); !errors.Is(e, ErrClosed) || effects.Load() != 0 || f.calls.Load() != 2 {
			t.Fatal("post-ack action forwarded")
		}
	})
	t.Run("lost revoke response is uncertain and holds later local actions", func(t *testing.T) {
		f := endpoint(t, func(r Request) []byte { return nil })
		qualify(t, f)
		if _, e := f.proxy.Exchange(context.Background(), base("revoke"), nil); !errors.Is(e, ErrUncertain) {
			t.Fatal("revoke uncertainty lost")
		}
		if _, e := f.proxy.Exchange(context.Background(), action(t), nil); !errors.Is(e, ErrClosed) || f.calls.Load() != 2 {
			t.Fatal("action after uncertain revoke")
		}
	})
	t.Run("wrong owner incarnation epoch peer and changed socket pins send no frame", func(t *testing.T) {
		f := endpoint(t, func(r Request) []byte { return nil })
		for _, mutate := range []func(*Request){func(r *Request) { r.Binding.PersonID = "other" }, func(r *Request) { r.Incarnation = generation }, func(r *Request) { r.Epoch = generation }} {
			r := base("observe")
			mutate(&r)
			if _, e := f.proxy.Exchange(context.Background(), r, nil); !errors.Is(e, ErrClosed) {
				t.Fatal("binding accepted")
			}
		}
		opts := f.options
		opts.PeerPID++
		p, e := New(opts)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.Exchange(context.Background(), base("observe"), nil); !errors.Is(e, ErrClosed) {
			t.Fatal("peer accepted")
		}
		opts = f.options
		opts.Socket.Inode++
		if _, e = New(opts); !errors.Is(e, ErrClosed) {
			t.Fatal("inode accepted")
		}
		if f.calls.Load() != 0 {
			t.Fatal("frame escaped admission")
		}
	})
	t.Run("writable private paths symlink and hardlinked socket are refused", func(t *testing.T) {
		f := endpoint(t, func(r Request) []byte { return nil })
		if os.Chmod(f.directory, 0755) != nil {
			t.Fatal("chmod")
		}
		if _, e := New(f.options); !errors.Is(e, ErrClosed) {
			t.Fatal("public directory accepted")
		}
		if os.Chmod(f.directory, 0700) != nil {
			t.Fatal("restore private directory")
		}
		if os.Chmod(f.options.SocketPath, 0660) != nil {
			t.Fatal("chmod")
		}
		if _, e := New(f.options); !errors.Is(e, ErrClosed) {
			t.Fatal("public socket accepted")
		}
		if os.Chmod(f.options.SocketPath, 0600) != nil {
			t.Fatal("restore private socket")
		}
		if _, e := New(f.options); e != nil {
			t.Fatal("original private socket must be admitted before link negatives")
		}
		link := filepath.Join(f.directory, "alias.sock")
		if os.Symlink(f.options.SocketPath, link) != nil {
			t.Fatal("symlink")
		}
		opts := f.options
		opts.SocketPath = link
		if _, e := New(opts); !errors.Is(e, ErrClosed) {
			t.Fatal("symlink accepted")
		}
		if os.Link(f.options.SocketPath, filepath.Join(f.directory, "hard.sock")) != nil {
			t.Fatal("synthetic hardlink must exist before refusal is tested")
		}
		info, err := os.Lstat(f.options.SocketPath)
		if err != nil {
			t.Fatal("inspect hardlinked socket")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSocket == 0 || stat.Nlink != 2 {
			t.Fatal("original socket must have exactly two links")
		}
		if _, e := New(f.options); !errors.Is(e, ErrClosed) {
			t.Fatal("hardlink accepted")
		}
		if f.calls.Load() != 0 {
			t.Fatal("path refusals must send no frame")
		}
	})
	t.Run("extra malformed oversized and private-error responses remain uncertain without retry", func(t *testing.T) {
		for _, kind := range []string{"trailing", "wrong_receipt", "oversized", "private_error", "empty"} {
			t.Run(kind, func(t *testing.T) {
				a := action(t)
				f := endpoint(t, func(r Request) []byte {
					m := envelope(r)
					m["status"] = "receipt"
					p := receiptObject(t, a)
					m["receipt"] = p
					switch kind {
					case "empty":
						return nil
					case "oversized":
						prefix := make([]byte, 4)
						binary.BigEndian.PutUint32(prefix, MaxPayload+1)
						return prefix
					case "private_error":
						p["code"] = "SYNTHETIC_PRIVATE_CANARY"
					case "wrong_receipt":
						p["deliveryId"] = generation
					}
					frame := framed(t, m)
					if kind == "trailing" {
						frame = append(frame, 0)
					}
					return frame
				})
				qualify(t, f)
				_, e := f.proxy.Exchange(context.Background(), a, nil)
				if !errors.Is(e, ErrUncertain) || strings.Contains(e.Error(), "CANARY") || f.calls.Load() != 2 {
					t.Fatal("failure retried or disclosed")
				}
			})
		}
	})
	t.Run("bounded cancellation after dispatch records uncertainty and never resends", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		entered := make(chan struct{})
		f := endpoint(t, func(r Request) []byte { close(entered); <-release; return nil })
		qualify(t, f)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		request := action(t)
		go func() { _, e := f.proxy.Exchange(ctx, request, nil); done <- e }()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("action was not dispatched")
		}
		cancel()
		select {
		case e := <-done:
			if !errors.Is(e, ErrUncertain) || f.calls.Load() != 2 {
				t.Fatal("cancelled dispatch retried or lost uncertainty")
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled socket did not close")
		}
	})
	t.Run("missing receipt refuses without claiming prior action did not enter", func(t *testing.T) {
		f := endpoint(t, func(r Request) []byte {
			m := envelope(r)
			m["status"] = "refused"
			m["code"] = "receipt_missing"
			return framed(t, m)
		})
		a := action(t)
		r := base("receipt")
		r.CommandID = a.CommandID
		r.DeliveryID = a.DeliveryID
		r.RequestSHA256, _ = ActionCommitment(a)
		reply, e := f.proxy.Exchange(context.Background(), r, &a)
		if e != nil || reply.Status != "refused" || reply.Receipt != nil {
			t.Fatal("missing receipt fabricated")
		}
	})
}

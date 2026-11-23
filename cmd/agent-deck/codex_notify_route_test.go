package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Root cause C of WO-DECK-CODEX-DELIVERY-01: Codex's shared app-server daemon
// fires every notify with the environment of the first TUI in its CODEX_HOME.
// These tests drive handleCodexNotify end to end against a synthetic session
// store and synthetic rollouts. No real ids, paths or content.

var routeThreadSeq int

// routeThread returns a UUID-shaped thread id unique in this test binary (the
// rollout metadata cache is keyed by thread id for the life of the process).
func routeThread() string {
	routeThreadSeq++
	return fmt.Sprintf("01b00000-0000-7000-8000-%012d", routeThreadSeq)
}

type codexRouteFixture struct {
	t         *testing.T
	home      string
	codexHome string
	storage   *session.Storage
}

func newCodexRouteFixture(t *testing.T, source codexNotifySource) *codexRouteFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CODEX_SESSION_ID", "")
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	restore := codexNotifySourceOf
	codexNotifySourceOf = func() codexNotifySource { return source }
	t.Cleanup(func() { codexNotifySourceOf = restore })

	storage, err := session.NewStorageWithProfile("codex_route")
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return &codexRouteFixture{t: t, home: home, codexHome: codexHome, storage: storage}
}

// project makes a project directory for a synthetic session.
func (f *codexRouteFixture) project(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "work", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

// session stores a Codex session bound to thread ("" for unbound).
func (f *codexRouteFixture) session(id, project, thread string) *session.Instance {
	f.t.Helper()
	inst := &session.Instance{
		ID: id, Title: id, Tool: "codex", Command: "codex",
		ProjectPath: project, CodexSessionID: thread, CreatedAt: time.Now(),
	}
	f.save(inst)
	return inst
}

func (f *codexRouteFixture) save(insts ...*session.Instance) {
	f.t.Helper()
	all, err := f.storage.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	all = append(all, insts...)
	if err := f.storage.SaveWithGroups(all, session.NewGroupTree(all)); err != nil {
		f.t.Fatalf("seed save: %v", err)
	}
}

// rollout writes a thread's session_meta under the Codex home, as Codex does
// when the thread's first turn starts. source is "cli" or "exec".
func (f *codexRouteFixture) rollout(thread, cwd, source string) {
	f.t.Helper()
	dir := filepath.Join(f.codexHome, "sessions", "2026", "11", "23")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{
		"id": thread, "session_id": thread, "cwd": cwd, "source": source, "thread_source": "user",
		"originator": "codex-tui",
	}})
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-11-23T10-00-00-"+thread+".jsonl"), append(meta, '\n'), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// notify runs one codex-notify with the environment of envInstance.
func (f *codexRouteFixture) notify(envInstance, payload string) {
	f.t.Helper()
	f.t.Setenv("AGENTDECK_INSTANCE_ID", envInstance)
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"agent-deck", "codex-notify", payload}
	handleCodexNotify()
}

func turnComplete(thread, cwd string) string {
	p := map[string]string{"type": "agent-turn-complete", "thread-id": thread, "turn-id": routeThread()}
	if cwd != "" {
		p["cwd"] = cwd
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// record reads the hook record filed under an instance, nil when none.
func (f *codexRouteFixture) record(instanceID string) *hookStatusFile {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(getHooksDir(), instanceID+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var rec hookStatusFile
	if err := json.Unmarshal(data, &rec); err != nil {
		f.t.Fatal(err)
	}
	return &rec
}

func (f *codexRouteFixture) lifecycle(action string) []session.SessionIDLifecycleEvent {
	f.t.Helper()
	file, err := os.Open(session.GetSessionIDLifecycleLogPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	var out []session.SessionIDLifecycleEvent
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var ev session.SessionIDLifecycleEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Action == action {
			out = append(out, ev)
		}
	}
	return out
}

// reload reads a stored session back and applies its hook record the way a
// fresh CLI process does.
func (f *codexRouteFixture) reload(id string) *session.Instance {
	f.t.Helper()
	all, err := f.storage.Load()
	if err != nil {
		f.t.Fatal(err)
	}
	for _, inst := range all {
		if inst.ID == id {
			session.ReloadHookStatus(inst)
			return inst
		}
	}
	f.t.Fatalf("session %s not stored", id)
	return nil
}

// The live incident: two sessions ride one daemon whose environment is the
// busy session's. The idle session's turn-end must land in the idle
// session's hook file, never the busy one's, and leave both bindings alone.
func TestCodexNotify_DaemonEventIsFiledUnderTheThreadOwner(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromDaemon)
	busyThread, idleThread := routeThread(), routeThread()
	busyDir, idleDir := f.project("busy"), f.project("idle")
	f.session("busy-0001", busyDir, busyThread)
	f.session("idle-0002", idleDir, idleThread)
	f.rollout(busyThread, busyDir, "cli")
	f.rollout(idleThread, idleDir, "cli")

	f.notify("busy-0001", turnComplete(idleThread, idleDir))

	if rec := f.record("busy-0001"); rec != nil {
		t.Fatalf("idle thread's turn-end filed under the busy session: %+v", rec)
	}
	rec := f.record("idle-0002")
	if rec == nil || rec.SessionID != idleThread || rec.Status != "waiting" {
		t.Fatalf("idle session's record = %+v, want its own waiting turn-end", rec)
	}
	if rec.CodexThreadEvidence != session.CodexThreadEvidenceBinding {
		t.Fatalf("evidence = %q, want %q", rec.CodexThreadEvidence, session.CodexThreadEvidenceBinding)
	}
	if got := session.ReadHookSessionAnchor("busy-0001"); got != "" {
		t.Fatalf("busy session's anchor moved to %q", got)
	}
	if got := session.ReadHookSessionAnchor("idle-0002"); got != idleThread {
		t.Fatalf("idle session's anchor = %q, want %q", got, idleThread)
	}
	routes := f.lifecycle("route")
	if len(routes) != 1 || routes[0].InstanceID != "idle-0002" || routes[0].RoutedFrom != "busy-0001" || routes[0].Candidate != idleThread {
		t.Fatalf("route log = %+v, want one busy-0001 -> idle-0002 entry", routes)
	}

	// The busy session's own turn still reaches it.
	f.notify("busy-0001", turnComplete(busyThread, busyDir))
	if rec := f.record("busy-0001"); rec == nil || rec.SessionID != busyThread {
		t.Fatalf("busy session's own turn-end = %+v", rec)
	}

	// Readers keep both bindings.
	if busy := f.reload("busy-0001"); busy.CodexSessionID != busyThread {
		t.Fatalf("busy binding = %q, want %q", busy.CodexSessionID, busyThread)
	}
	if idle := f.reload("idle-0002"); idle.CodexSessionID != idleThread {
		t.Fatalf("idle binding = %q, want %q", idle.CodexSessionID, idleThread)
	}
}

// A daemon event for a thread no session is bound to cannot be attributed:
// the environment names the daemon's first TUI, bound or not.
func TestCodexNotify_DaemonEventForUnboundThreadIsDropped(t *testing.T) {
	for _, envBound := range []bool{true, false} {
		t.Run(fmt.Sprintf("env_bound=%v", envBound), func(t *testing.T) {
			f := newCodexRouteFixture(t, codexNotifyFromDaemon)
			envThread, stray := "", routeThread()
			if envBound {
				envThread = routeThread()
			}
			dir := f.project("first")
			f.session("first-0003", dir, envThread)
			f.rollout(stray, dir, "cli")

			f.notify("first-0003", turnComplete(stray, dir))

			if rec := f.record("first-0003"); rec != nil {
				t.Fatalf("unattributable daemon event filed under the env session: %+v", rec)
			}
			drops := f.lifecycle("drop")
			if len(drops) != 1 || drops[0].Reason != "thread_bound_to_no_session" || drops[0].Candidate != stray {
				t.Fatalf("drop log = %+v", drops)
			}
		})
	}
}

// Without a thread id the notify falls back to the inherited
// CODEX_SESSION_ID, which for the daemon is its first TUI's thread.
func TestCodexNotify_DaemonEventWithoutThreadIsDropped(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromDaemon)
	envThread := routeThread()
	dir := f.project("first")
	f.session("first-0004", dir, envThread)
	f.rollout(envThread, dir, "cli")
	t.Setenv("CODEX_SESSION_ID", envThread)

	f.notify("first-0004", `{"type":"agent-turn-complete"}`)

	if rec := f.record("first-0004"); rec != nil {
		t.Fatalf("identity-less daemon event filed under the env session: %+v", rec)
	}
}

func TestCodexNotify_DaemonEventForContestedThread(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromDaemon)
	contested, single := routeThread(), routeThread()
	dir := f.project("shared")
	f.session("a-0005", dir, contested)
	f.session("b-0006", dir, contested)
	f.rollout(contested, dir, "cli")
	f.notify("a-0005", turnComplete(contested, dir))
	if a, b := f.record("a-0005"), f.record("b-0006"); a != nil || b != nil {
		t.Fatalf("a thread bound to two live sessions picked one: a=%+v b=%+v", a, b)
	}

	// An archived session's stale binding is not a claim.
	f.session("live-0007", dir, single)
	gone := &session.Instance{ID: "gone-0008", Title: "gone", Tool: "codex", Command: "codex",
		ProjectPath: dir, CodexSessionID: single, CreatedAt: time.Now(), ArchivedAt: time.Now()}
	f.save(gone)
	f.rollout(single, dir, "cli")
	f.notify("a-0005", turnComplete(single, dir))
	if rec := f.record("live-0007"); rec == nil || rec.SessionID != single {
		t.Fatalf("live owner's record = %+v, want the routed turn-end", rec)
	}
	if rec := f.record("gone-0008"); rec != nil {
		t.Fatalf("archived session received a record: %+v", rec)
	}
}

// A thread's cwd is where its session started it. A binding whose session
// owns no such path is itself wrong (a sibling's thread), so the event goes
// nowhere rather than to that session.
func TestCodexNotify_DaemonEventRefusesOwnerOutsideThreadCwd(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromDaemon)
	thread := routeThread()
	busyDir, idleDir := f.project("busy"), f.project("idle")
	f.session("busy-0009", busyDir, thread) // poisoned: the idle session's thread
	f.rollout(thread, idleDir, "cli")

	f.notify("busy-0009", turnComplete(thread, idleDir))

	if rec := f.record("busy-0009"); rec != nil {
		t.Fatalf("event routed to a session that owns no path of the thread's cwd: %+v", rec)
	}
	if drops := f.lifecycle("drop"); len(drops) != 1 || drops[0].Reason != "thread_cwd_outside_bound_session" {
		t.Fatalf("drop log = %+v", drops)
	}
}

// /new in an in-process Codex pane: the notify's parent is the pane's own
// TUI, so the env session is the one running the new thread. The record says
// so and the reader follows the pane to the new thread.
func TestCodexNotify_PaneEventMovesItsSessionToTheNewThread(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromPane)
	old, next := routeThread(), routeThread()
	dir := f.project("pane")
	f.session("pane-0010", dir, old)
	f.rollout(old, dir, "cli")
	f.rollout(next, dir, "cli")

	f.notify("pane-0010", turnComplete(next, dir))

	rec := f.record("pane-0010")
	if rec == nil || rec.SessionID != next || rec.CodexThreadEvidence != session.CodexThreadEvidencePane {
		t.Fatalf("record = %+v, want the new thread with pane evidence", rec)
	}
	if inst := f.reload("pane-0010"); inst.CodexSessionID != next {
		t.Fatalf("binding = %q, want the pane's new thread %q", inst.CodexSessionID, next)
	}
}

// A fresh session's first turn binds it, from the pane as before.
func TestCodexNotify_PaneEventBindsAFreshSession(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromPane)
	first := routeThread()
	dir := f.project("fresh")
	f.session("fresh-0011", dir, "")
	f.rollout(first, dir, "cli")

	f.notify("fresh-0011", turnComplete(first, dir))

	if inst := f.reload("fresh-0011"); inst.CodexSessionID != first {
		t.Fatalf("fresh session binding = %q, want %q", inst.CodexSessionID, first)
	}
}

// A `codex exec` run inside the pane inherits its environment. Its turn-end
// may still be filed there, but carries no evidence, so it never moves the
// pane's binding.
func TestCodexNotify_PaneExecThreadNeverMovesTheBinding(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromPane)
	own, child := routeThread(), routeThread()
	dir := f.project("pane")
	f.session("pane-0012", dir, own)
	f.rollout(own, dir, "cli")
	f.rollout(child, dir, "exec")

	f.notify("pane-0012", turnComplete(child, dir))

	rec := f.record("pane-0012")
	if rec == nil || rec.CodexThreadEvidence != "" {
		t.Fatalf("exec child's record = %+v, want it filed without evidence", rec)
	}
	if inst := f.reload("pane-0012"); inst.CodexSessionID != own {
		t.Fatalf("binding = %q, want %q", inst.CodexSessionID, own)
	}
}

// When the parent cannot be classified, the store still decides: a bound
// thread goes to its session, an unbound env session may bootstrap, and an
// env session bound elsewhere does not take a stranger's thread.
func TestCodexNotify_UnknownSourceUsesTheStore(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromUnknown)
	ownerThread, fresh, stray := routeThread(), routeThread(), routeThread()
	ownerDir, envDir := f.project("owner"), f.project("env")
	f.session("owner-0013", ownerDir, ownerThread)
	f.session("fresh-0014", envDir, "")
	boundThread := routeThread()
	f.session("bound-0015", envDir, boundThread)
	f.rollout(ownerThread, ownerDir, "cli")
	f.rollout(fresh, envDir, "cli")
	f.rollout(stray, envDir, "cli")

	f.notify("fresh-0014", turnComplete(ownerThread, ownerDir))
	if rec := f.record("owner-0013"); rec == nil || rec.CodexThreadEvidence != session.CodexThreadEvidenceBinding {
		t.Fatalf("owner record = %+v, want the routed turn-end", rec)
	}
	if rec := f.record("fresh-0014"); rec != nil {
		t.Fatalf("owned thread filed under the env session: %+v", rec)
	}

	f.notify("fresh-0014", turnComplete(fresh, envDir))
	if rec := f.record("fresh-0014"); rec == nil || rec.SessionID != fresh || rec.CodexThreadEvidence != "" {
		t.Fatalf("fresh session record = %+v, want its bootstrap turn without evidence", rec)
	}
	if inst := f.reload("fresh-0014"); inst.CodexSessionID != fresh {
		t.Fatalf("fresh binding = %q, want %q", inst.CodexSessionID, fresh)
	}

	f.notify("bound-0015", turnComplete(stray, envDir))
	if rec := f.record("bound-0015"); rec != nil {
		t.Fatalf("a stranger's thread filed under a session bound elsewhere: %+v", rec)
	}
}

// A host with no session store at all (a sandbox, a remote host) keeps the
// old destination for the unclassifiable case, without evidence. Stores that
// exist but cannot be read may hold the owner, so the event is dropped.
func TestCodexNotify_UnknownSourceWithoutStoreKeepsTheOldDestination(t *testing.T) {
	f := newCodexRouteFixture(t, codexNotifyFromUnknown)
	thread := routeThread()
	f.rollout(thread, f.project("x"), "cli")
	restore := codexThreadLookup
	t.Cleanup(func() { codexThreadLookup = restore })
	codexThreadLookup = func(string, string) (session.CodexThreadLookup, error) {
		return session.CodexThreadLookup{}, fmt.Errorf("codex thread lookup: %w", session.ErrNoCodexThreadStore)
	}

	f.notify("env-0016", turnComplete(thread, ""))
	if rec := f.record("env-0016"); rec == nil || rec.SessionID != thread || rec.CodexThreadEvidence != "" {
		t.Fatalf("record = %+v, want the env session's record without evidence", rec)
	}

	// The daemon's environment is no evidence at all: nothing is filed.
	codexNotifySourceOf = func() codexNotifySource { return codexNotifyFromDaemon }
	f.notify("env-0017", turnComplete(thread, ""))
	if rec := f.record("env-0017"); rec != nil {
		t.Fatalf("daemon event filed without a readable store: %+v", rec)
	}

	// Stores present but unreadable: dropped from the unknown source too.
	codexNotifySourceOf = func() codexNotifySource { return codexNotifyFromUnknown }
	codexThreadLookup = func(string, string) (session.CodexThreadLookup, error) {
		return session.CodexThreadLookup{}, errors.New("codex thread lookup: no readable session store: file is not a database")
	}
	f.notify("env-0021", turnComplete(thread, ""))
	if rec := f.record("env-0021"); rec != nil {
		t.Fatalf("event filed under the env session while existing stores were unreadable: %+v", rec)
	}
}

func TestClassifyCodexNotifySource(t *testing.T) {
	const tui = "/opt/codex/vendor/x86_64-unknown-linux-musl/bin/codex"
	const daemonBin = "/home/u/.codex/packages/app-server-daemon/releases/0.171.0/bin/codex"
	cases := []struct {
		name  string
		procs map[int][]string // pid -> argv
		ppids map[int]int
		want  codexNotifySource
	}{
		{"in-process TUI", map[int][]string{100: {tui, "--yolo", "resume", "01b0"}}, nil, codexNotifyFromPane},
		{"exec child", map[int][]string{100: {tui, "exec", "task"}}, nil, codexNotifyFromPane},
		{"shared daemon", map[int][]string{100: {daemonBin, "app-server", "--listen", "unix://"}}, nil, codexNotifyFromDaemon},
		{"daemon from another install", map[int][]string{100: {tui, "app-server", "--listen", "unix://"}}, nil, codexNotifyFromDaemon},
		{"daemon package binary", map[int][]string{100: {daemonBin, "--listen"}}, nil, codexNotifyFromDaemon},
		{"managed daemon flag alone", map[int][]string{100: {tui, "serve", "--managed-daemon"}}, nil, codexNotifyFromDaemon},
		{"through env and sh", map[int][]string{100: {"/usr/bin/env", "X=1", "agent-deck"}, 90: {"sh", "-c", "x"}, 80: {daemonBin, "app-server"}},
			map[int]int{100: 90, 90: 80}, codexNotifyFromDaemon},
		{"not codex", map[int][]string{100: {"/usr/bin/python3", "wrapper.py"}}, nil, codexNotifyFromUnknown},
		// An interpreter running a script named after codex is not the Codex
		// binary: its parent may be the daemon, so its env is no evidence.
		{"script named codex", map[int][]string{100: {"/usr/bin/python3", "/opt/codex_notify_bridge.py"}}, nil, codexNotifyFromUnknown},
		// Behind a launcher, Codex is not trusted as a pane, but its daemon
		// markers still count: reading it as the daemon withholds trust.
		{"node running the codex TUI", map[int][]string{100: {"/usr/bin/node", "/opt/bin/codex", "--yolo"}}, nil, codexNotifyFromUnknown},
		{"node running the codex daemon", map[int][]string{100: {"/usr/bin/node", "/opt/bin/codex", "app-server"}}, nil, codexNotifyFromDaemon},
		{"unreadable parent", map[int][]string{}, nil, codexNotifyFromUnknown},
		{"wrapper chain too long", map[int][]string{100: {"sh"}, 99: {"sh"}, 98: {"sh"}, 97: {"sh"}, 96: {tui}},
			map[int]int{100: 99, 99: 98, 98: 97, 97: 96}, codexNotifyFromUnknown},
	}
	restore := codexNotifyProcs
	t.Cleanup(func() { codexNotifyProcs = restore })
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codexNotifyProcs.parent = func() int { return 100 }
			codexNotifyProcs.argv = func(pid int) ([]string, error) {
				if argv, ok := tc.procs[pid]; ok {
					return argv, nil
				}
				return nil, errors.New("no such process")
			}
			codexNotifyProcs.ppid = func(pid int) (int, error) {
				if p, ok := tc.ppids[pid]; ok {
					return p, nil
				}
				return 0, errors.New("no parent")
			}
			if got := classifyCodexNotifySource(); got != tc.want {
				t.Fatalf("classifyCodexNotifySource() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ps joins argv with spaces. A Codex home under a path with spaces must still
// yield the executable as one argument, or a daemon reads as unknown.
func TestArgvFromPS(t *testing.T) {
	const daemon = "/Users/First Last/.codex/packages/app-server-daemon/releases/0.171.0/bin/codex"
	cases := []struct {
		name, comm, args string
		want             []string
	}{
		{"spaced executable", daemon, daemon + " app-server --listen unix://", []string{daemon, "app-server", "--listen", "unix://"}},
		{"comm is the bare name", "codex", "/opt/bin/codex --yolo resume 01b0", []string{"codex", "--yolo", "resume", "01b0"}},
		{"no comm", "", "/opt/bin/codex exec task", []string{"/opt/bin/codex", "exec", "task"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := argvFromPS(tc.comm+"\n", tc.args+"\n")
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("argvFromPS = %q, want %q", got, tc.want)
			}
		})
	}
	if !isCodexAppServerArgv(argvFromPS(daemon, daemon+" app-server --listen unix://")) {
		t.Fatal("a daemon under a spaced home must classify as the app-server")
	}
}

// Pane evidence needs the thread's rollout to show an interactive
// conversation. Without a Codex home, or before the rollout is flushed, an
// exec child's thread cannot be told apart: the record carries no evidence.
func TestRouteCodexNotify_PaneWithoutInteractiveRolloutCarriesNoEvidence(t *testing.T) {
	for _, home := range []string{"", t.TempDir()} {
		route := routeCodexNotify("pane-0018", routeThread(), true, "", codexNotifyFromPane, home)
		if route.instanceID != "pane-0018" || route.evidence != "" {
			t.Fatalf("home %q: route = %+v, want the env session without evidence", home, route)
		}
	}
}

// A store that could not be read may hold the thread's owner. An unowned
// answer is then not trusted for the env fallback, while an owner that was
// found is still routed to.
func TestRouteCodexNotify_IncompleteStoreLookup(t *testing.T) {
	thread := routeThread()
	restore := codexThreadLookup
	t.Cleanup(func() { codexThreadLookup = restore })

	codexThreadLookup = func(string, string) (session.CodexThreadLookup, error) {
		return session.CodexThreadLookup{Incomplete: true}, nil
	}
	for _, source := range []codexNotifySource{codexNotifyFromUnknown, codexNotifyFromDaemon} {
		if route := routeCodexNotify("env-0019", thread, true, "", source, ""); route.instanceID != "" ||
			route.reason != "session_store_incomplete" {
			t.Fatalf("%v source, incomplete and unowned: route = %+v, want a drop that says the store was incomplete", source, route)
		}
	}

	codexThreadLookup = func(string, string) (session.CodexThreadLookup, error) {
		return session.CodexThreadLookup{Incomplete: true, Owners: []session.CodexThreadBinding{{InstanceID: "owner-0020", Tool: "codex"}}}, nil
	}
	for _, source := range []codexNotifySource{codexNotifyFromUnknown, codexNotifyFromDaemon} {
		route := routeCodexNotify("env-0019", thread, true, "", source, "")
		if route.instanceID != "owner-0020" || route.evidence != session.CodexThreadEvidenceBinding {
			t.Fatalf("%v source, incomplete with an owner: route = %+v, want the owner", source, route)
		}
	}
}

func TestProcessArgvAndParentOfSelf(t *testing.T) {
	argv, err := processArgv(os.Getpid())
	if err != nil || len(argv) == 0 {
		t.Fatalf("processArgv(self) = %v, %v", argv, err)
	}
	ppid, err := processParentPID(os.Getpid())
	if err != nil || ppid != os.Getppid() {
		t.Fatalf("processParentPID(self) = %d, %v; want %d", ppid, err, os.Getppid())
	}
}

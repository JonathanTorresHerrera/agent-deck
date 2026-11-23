package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Codex notify writer's read-only store lookup (codex_thread_lookup.go).
// All ids and paths are synthetic.

// isolateProfileStores points every profile store at a fresh directory.
func isolateProfileStores(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("AGENTDECK_PROFILE", "")
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	return home
}

func seedProfileInstances(t *testing.T, profile string, insts ...*Instance) *Storage {
	t.Helper()
	storage, err := NewStorageWithProfile(profile)
	if err != nil {
		t.Fatalf("NewStorageWithProfile(%s): %v", profile, err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if err := storage.SaveWithGroups(insts, NewGroupTree(insts)); err != nil {
		t.Fatalf("seed %s: %v", profile, err)
	}
	return storage
}

func TestLookupCodexThreadBindings(t *testing.T) {
	home := isolateProfileStores(t)
	shared, archived := uniqueSID(t), uniqueSID(t)
	projA := filepath.Join(home, "proj-a")
	projB := filepath.Join(home, "proj-b")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(filepath.Join(p, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ownerA := NewInstanceWithTool("owner-a", projA, "codex")
	ownerA.CodexSessionID = shared
	unbound := NewInstanceWithTool("fresh", projA, "codex")
	gone := NewInstanceWithTool("archived", projA, "codex")
	gone.CodexSessionID = archived
	gone.ArchivedAt = time.Now()
	claude := NewInstanceWithTool("claude", projA, "claude")
	claude.CodexSessionID = shared // a stale key on a non-Codex row owns nothing
	broken := NewInstanceWithTool("broken", projA, "codex")
	storeA := seedProfileInstances(t, "lookup_a", ownerA, unbound, gone, claude, broken)
	// A row whose tool_data is not JSON must neither match nor fail the query.
	if _, err := storeA.GetDB().DB().Exec(`UPDATE instances SET tool_data = ? WHERE id = ?`, "{not json", broken.ID); err != nil {
		t.Fatal(err)
	}
	ownerB := NewInstanceWithTool("owner-b", projB, "codex")
	ownerB.CodexSessionID = shared
	seedProfileInstances(t, "lookup_b", ownerB)

	got, err := LookupCodexThreadBindings(shared, unbound.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]bool{}
	for _, o := range got.Owners {
		owners[o.InstanceID] = true
	}
	if len(got.Owners) != 2 || !owners[ownerA.ID] || !owners[ownerB.ID] {
		t.Fatalf("owners = %+v, want exactly %s and %s", got.Owners, ownerA.ID, ownerB.ID)
	}
	if got.Env == nil || got.Env.InstanceID != unbound.ID || got.Env.CodexSessionID != "" {
		t.Fatalf("env row = %+v, want the unbound session", got.Env)
	}

	got, err = LookupCodexThreadBindings(archived, ownerA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Owners) != 0 {
		t.Fatalf("an archived session owns no live thread: %+v", got.Owners)
	}
	if got.Env == nil || got.Env.CodexSessionID != shared {
		t.Fatalf("env row = %+v, want owner-a bound to the shared thread", got.Env)
	}
	if got.Env.CwdIsForeign(filepath.Join(projA, "sub")) || got.Env.CwdIsForeign("") {
		t.Fatal("a cwd inside the project, or no cwd, is not foreign")
	}
	if !got.Env.CwdIsForeign(projB) {
		t.Fatal("a sibling project's cwd must be foreign")
	}
}

func TestLookupCodexThreadBindingsWithoutStoreFails(t *testing.T) {
	isolateProfileStores(t)
	if _, err := LookupCodexThreadBindings(uniqueSID(t), "nobody"); err == nil {
		t.Fatal("no readable store must be an error, not an empty answer")
	}
}

func TestLookupCodexThreadBindingsReadsCommittedWAL(t *testing.T) {
	isolateProfileStores(t)
	thread := uniqueSID(t)
	inst := NewInstanceWithTool("wal", "/tmp/wal", "codex")
	storage := seedProfileInstances(t, "lookup_wal", inst)
	// A binding written through json_set and still in the WAL (the writer
	// keeps its connection open) must be visible to the notify's lookup.
	if err := storage.GetDB().WriteCodexSessionBinding(inst.ID, thread, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := LookupCodexThreadBindings(thread, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Owners) != 1 || got.Owners[0].InstanceID != inst.ID {
		t.Fatalf("owners = %+v, want the freshly bound session", got.Owners)
	}
	if got.Owners[0].Profile != "lookup_wal" {
		t.Fatalf("profile = %q, want lookup_wal", got.Owners[0].Profile)
	}
}

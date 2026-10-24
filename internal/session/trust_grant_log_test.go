package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/logging"
)

// trustGrantRecords decodes buf and returns every folder_trust_granted record.
func trustGrantRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}
		if rec["msg"] == "folder_trust_granted" {
			out = append(out, rec)
		}
	}
	return out
}

// assertOneTrustGrant checks buf holds exactly one folder_trust_granted record
// naming tool, dir and the given location attribute.
func assertOneTrustGrant(t *testing.T, buf *bytes.Buffer, tool, dir, whereKey, where string) {
	t.Helper()
	recs := trustGrantRecords(t, buf)
	if len(recs) != 1 {
		t.Fatalf("folder_trust_granted records = %d, want 1:\n%s", len(recs), buf.String())
	}
	rec := recs[0]
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
	if rec["tool"] != tool {
		t.Errorf("tool = %v, want %q", rec["tool"], tool)
	}
	if rec["dir"] != dir {
		t.Errorf("dir = %v, want %q", rec["dir"], dir)
	}
	if rec[whereKey] != where {
		t.Errorf("%s = %v, want %q", whereKey, rec[whereKey], where)
	}
}

func TestFolderTrustGrantLog_Claude(t *testing.T) {
	buf := captureSessionLogJSON(t)
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	dir := filepath.Join(t.TempDir(), "proj")

	if err := PreAcceptClaudeTrust(cfg, dir); err != nil {
		t.Fatalf("first PreAcceptClaudeTrust: %v", err)
	}
	assertOneTrustGrant(t, buf, "claude", dir, "config", cfg)

	buf.Reset()
	if err := PreAcceptClaudeTrust(cfg, dir); err != nil {
		t.Fatalf("second PreAcceptClaudeTrust: %v", err)
	}
	if recs := trustGrantRecords(t, buf); len(recs) != 0 {
		t.Fatalf("already-trusted dir logged %d grants:\n%s", len(recs), buf.String())
	}
}

func TestFolderTrustGrantLog_ClaudePreexistingTrust(t *testing.T) {
	buf := captureSessionLogJSON(t)
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	dir := filepath.Join(t.TempDir(), "proj")
	seed, _ := json.Marshal(map[string]any{"projects": map[string]any{dir: map[string]any{"hasTrustDialogAccepted": true}}})
	if err := os.WriteFile(cfg, seed, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := PreAcceptClaudeTrust(cfg, dir); err != nil {
		t.Fatalf("PreAcceptClaudeTrust: %v", err)
	}
	if recs := trustGrantRecords(t, buf); len(recs) != 0 {
		t.Fatalf("pre-trusted dir logged %d grants:\n%s", len(recs), buf.String())
	}
}

func TestFolderTrustGrantLog_Codex(t *testing.T) {
	buf := captureSessionLogJSON(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")
	dir := filepath.Join(t.TempDir(), "proj")

	if err := PreAcceptCodexTrust(cfg, dir); err != nil {
		t.Fatalf("first PreAcceptCodexTrust: %v", err)
	}
	assertOneTrustGrant(t, buf, "codex", dir, "config", cfg)

	buf.Reset()
	if err := PreAcceptCodexTrust(cfg, dir); err != nil {
		t.Fatalf("second PreAcceptCodexTrust: %v", err)
	}
	if recs := trustGrantRecords(t, buf); len(recs) != 0 {
		t.Fatalf("already-trusted dir logged %d grants:\n%s", len(recs), buf.String())
	}
}

func TestFolderTrustGrantLog_CursorLocal(t *testing.T) {
	buf := captureSessionLogJSON(t)
	cursorDir := filepath.Join(t.TempDir(), ".cursor")
	dir := filepath.Join(t.TempDir(), "proj")
	trustPath, _, err := cursorWorkspaceTrustPath(cursorDir, dir)
	if err != nil {
		t.Fatalf("cursorWorkspaceTrustPath: %v", err)
	}

	if err := PreAcceptCursorTrust(cursorDir, dir); err != nil {
		t.Fatalf("first PreAcceptCursorTrust: %v", err)
	}
	assertOneTrustGrant(t, buf, "cursor", dir, "config", trustPath)

	buf.Reset()
	if err := PreAcceptCursorTrust(cursorDir, dir); err != nil {
		t.Fatalf("second PreAcceptCursorTrust: %v", err)
	}
	if recs := trustGrantRecords(t, buf); len(recs) != 0 {
		t.Fatalf("already-trusted dir logged %d grants:\n%s", len(recs), buf.String())
	}
}

func TestFolderTrustGrantLog_CursorSSH(t *testing.T) {
	fakeBin := t.TempDir()
	remoteHome := t.TempDir()
	// Fake ssh: drop -o options and the host, then run the script locally
	// with HOME pointed at a temp "remote" home.
	if err := os.WriteFile(filepath.Join(fakeBin, "ssh"), []byte(`#!/bin/sh
while [ "$1" = "-o" ]; do shift 2; done
shift
export HOME="$FAKE_REMOTE_HOME"
exec sh -s
`), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_REMOTE_HOME", remoteHome)
	buf := captureSessionLogJSON(t)
	dir := "/remote/proj"

	if err := PreAcceptCursorTrustSSH("user@remote", dir); err != nil {
		t.Fatalf("first PreAcceptCursorTrustSSH: %v", err)
	}
	assertOneTrustGrant(t, buf, "cursor", dir, "host", "user@remote")

	buf.Reset()
	if err := PreAcceptCursorTrustSSH("user@remote", dir); err != nil {
		t.Fatalf("second PreAcceptCursorTrustSSH: %v", err)
	}
	if recs := trustGrantRecords(t, buf); len(recs) != 0 {
		t.Fatalf("already-trusted dir logged %d grants:\n%s", len(recs), buf.String())
	}
}

func TestFolderTrustGrantLog_SanitizesValues(t *testing.T) {
	buf := captureSessionLogJSON(t)
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	dir := "/tmp/proj\nlevel=ERROR msg=forged\r"

	if err := PreAcceptClaudeTrust(cfg, dir); err != nil {
		t.Fatalf("PreAcceptClaudeTrust: %v", err)
	}
	assertOneTrustGrant(t, buf, "claude", logging.SanitizeValue(dir), "config", cfg)
	if d, _ := trustGrantRecords(t, buf)[0]["dir"].(string); strings.ContainsAny(d, "\n\r") {
		t.Errorf("dir carries a raw control character: %q", d)
	}
}

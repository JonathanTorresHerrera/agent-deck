package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testTrustRoots = []string{"/home/jtorres", "/mnt/d/Dev_Projects"}

func TestPathWithinTrustRoots(t *testing.T) {
	cases := map[string]bool{
		"/home/jtorres":                        true,
		"/home/jtorres/adk-build":              true,
		"/mnt/d/Dev_Projects/omni-prime/.wt/x": true,
		`D:\Dev_Projects\agent-deck-mcp\.wt\x`: true,  // Claude on Windows shows the drive spelling
		"/home/jtorresx":                       false, // sibling sharing the prefix
		"/home/jtorres/../../etc":              false,
		"/mnt/d/Other":                         false,
		"/mnt/d/Dev_ProjectsX/x":               false,
		"/tmp/x":                               false,
		`C:\Users\jonat`:                       false,
		"relative/path":                        false,
		"":                                     false,
		// WO-DECK-SWITCH-SIDECAR-STALE-01: Windows-native Claude also shows
		// the WSL UNC spelling of a Linux-side path, both backslash- and
		// forward-slash-separated, and under both wsl.localhost and wsl$.
		`\\wsl.localhost\Ubuntu\home\jtorres\adk-build\.worktrees\x`: true,
		`//wsl.localhost/Ubuntu/home/jtorres/adk-build/.worktrees/x`: true,
		`\\wsl$\Ubuntu\home\jtorres\adk-build\.worktrees\x`:          true,
		`\\wsl.localhost\Ubuntu\etc\passwd`:                          false, // outside roots even once normalized
		`\\wsl.localhost\Ubuntu\home\jtorresx`:                       false, // sibling sharing the prefix, UNC form
	}
	for p, want := range cases {
		if got := PathWithinTrustRoots(p, testTrustRoots); got != want {
			t.Errorf("PathWithinTrustRoots(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestPathWithinTrustRoots_SymlinkOutOfRootIsRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	if PathWithinTrustRoots(link, []string{root}) {
		t.Fatal("a symlink inside the root that resolves outside it was accepted")
	}
	inside := filepath.Join(root, "real")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if !PathWithinTrustRoots(inside, []string{root}) {
		t.Fatal("a real folder inside the root was refused")
	}
}

// fakeTrustPane renders a two-option trust dialog and reacts to Up/Down/Enter
// the way the TUIs do: Enter on the trust option clears the dialog, Enter on
// the other option is recorded as a refusal (it must never happen).
type fakeTrustPane struct {
	tool, path string
	cursor     int // 0 = first option
	accept     int // index of the trust option
	frozen     bool
	keys       []string
	answered   string
}

func (p *fakeTrustPane) CapturePaneFresh() (string, error) {
	if p.answered != "" {
		return "› Ask Codex to do anything\n", nil
	}
	opts := []string{"No, exit", "Yes, I trust this folder"}
	header := " Accessing workspace:\n " + p.path + "\n Quick safety check: Is this a project you created or one you trust?\n"
	footer := " Enter to confirm · Esc to cancel"
	if p.tool == "codex" {
		opts = []string{"1. Trust and continue", "2. Back to Agent Command Center"}
		header = "  Folder access\n  " + p.path + "\n  Trust this folder? Codex can read, edit, and run files here.\n"
		footer = "  enter continue · esc back"
	}
	var b strings.Builder
	b.WriteString(header)
	for i, o := range opts {
		if i == p.cursor {
			b.WriteString(" ❯ " + o + "\n")
		} else {
			b.WriteString("   " + o + "\n")
		}
	}
	b.WriteString(footer)
	return b.String(), nil
}

func (p *fakeTrustPane) SendNamedKey(key string) error {
	p.keys = append(p.keys, key)
	if p.frozen {
		return nil
	}
	switch key {
	case "Down":
		p.cursor = min(p.cursor+1, 1)
	case "Up":
		p.cursor = max(p.cursor-1, 0)
	case "Enter":
		if p.cursor == p.accept {
			p.answered = "trusted"
		} else {
			p.answered = "refused"
		}
	}
	return nil
}

func noSleep(time.Duration) {}

func TestAcceptTrustDialog_ClaudeMovesOffNoExitThenConfirms(t *testing.T) {
	pane := &fakeTrustPane{tool: "claude", path: "/home/jtorres/proj", cursor: 0, accept: 1}
	res := acceptTrustDialog(pane, "/home/jtorres/proj", testTrustRoots, noSleep)
	if res.Outcome != TrustAccepted || pane.answered != "trusted" {
		t.Fatalf("outcome %+v answered %q keys %v", res, pane.answered, pane.keys)
	}
	if strings.Join(pane.keys, ",") != "Down,Enter" {
		t.Fatalf("keys = %v, want Down then Enter (never Enter on No, exit)", pane.keys)
	}
}

func TestAcceptTrustDialog_CodexCursorAlreadyOnTrust(t *testing.T) {
	pane := &fakeTrustPane{tool: "codex", path: "/mnt/d/Dev_Projects/omni-prime", cursor: 0, accept: 0}
	res := acceptTrustDialog(pane, "/mnt/d/Dev_Projects/omni-prime", testTrustRoots, noSleep)
	if res.Outcome != TrustAccepted || strings.Join(pane.keys, ",") != "Enter" || res.Tool != "codex" {
		t.Fatalf("outcome %+v keys %v", res, pane.keys)
	}
}

func TestAcceptTrustDialog_OutsideRootsIsNeverAccepted(t *testing.T) {
	cases := []struct {
		name, workDir, dialogPath string
	}{
		{"session folder outside the roots", "/tmp/scratch", "/tmp/scratch"},
		{"session inside, dialog names a folder outside", "/home/jtorres/proj", "/etc"},
		{"windows path outside the roots", `C:\Users\jonat\x`, `C:\Users\jonat\x`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pane := &fakeTrustPane{tool: "claude", path: tc.dialogPath, cursor: 0, accept: 1}
			res := acceptTrustDialog(pane, tc.workDir, testTrustRoots, noSleep)
			if res.Outcome != TrustRefusedOutsideRoots {
				t.Fatalf("outcome = %+v, want refused_outside_roots", res)
			}
			if len(pane.keys) != 0 || pane.answered != "" {
				t.Fatalf("keys were sent outside the roots: %v", pane.keys)
			}
		})
	}
}

func TestAcceptTrustDialog_NoDialogSendsNothing(t *testing.T) {
	pane := &fakeTrustPane{answered: "already"}
	if res := acceptTrustDialog(pane, "/home/jtorres/p", testTrustRoots, noSleep); res.Outcome != TrustNotPresent || len(pane.keys) != 0 {
		t.Fatalf("outcome %+v keys %v", res, pane.keys)
	}
}

func TestAcceptTrustDialog_CursorThatWillNotMoveNeverPressesEnter(t *testing.T) {
	pane := &fakeTrustPane{tool: "claude", path: "/home/jtorres/p", cursor: 0, accept: 1, frozen: true}
	res := acceptTrustDialog(pane, "/home/jtorres/p", testTrustRoots, noSleep)
	if res.Outcome != TrustAcceptFailed {
		t.Fatalf("outcome = %+v", res)
	}
	for _, k := range pane.keys {
		if k == "Enter" {
			t.Fatalf("Enter pressed with the cursor off the trust option: %v", pane.keys)
		}
	}
}

type errPane struct{}

func (errPane) CapturePaneFresh() (string, error) { return "", errors.New("gone") }
func (errPane) SendNamedKey(string) error         { return nil }

func TestAcceptTrustDialog_CaptureFailure(t *testing.T) {
	if res := acceptTrustDialog(errPane{}, "/home/jtorres/p", testTrustRoots, noSleep); res.Outcome != TrustAcceptFailed {
		t.Fatalf("outcome = %+v", res)
	}
}

// Pre-trust before launch: the root ~/.claude.json AND the seat's own
// $CLAUDE_CONFIG_DIR/.claude.json get projects[dir].hasTrustDialogAccepted.
func TestClaudeTrustConfigPaths_SeatGetsTheEntryToo(t *testing.T) {
	home := t.TempDir()
	seat := filepath.Join(home, "seats", "claude-2")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", seat)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	inst := &Instance{ID: "t1", Tool: "claude", ProjectPath: filepath.Join(home, "proj")}
	paths := ClaudeTrustConfigPaths(inst)
	want := []string{filepath.Join(home, ".claude.json"), filepath.Join(seat, ".claude.json")}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for _, p := range paths {
		if err := PreAcceptClaudeTrust(p, inst.ProjectPath); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Projects map[string]map[string]any `json:"projects"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Projects[inst.ProjectPath]["hasTrustDialogAccepted"] != true {
			t.Fatalf("%s has no trust entry for %s: %s", p, inst.ProjectPath, data)
		}
	}
}

func TestClaudeTrustConfigPaths_DefaultDirIsRootOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	paths := ClaudeTrustConfigPaths(&Instance{ID: "t2", Tool: "claude"})
	if len(paths) != 1 || paths[0] != filepath.Join(home, ".claude.json") {
		t.Fatalf("paths = %v", paths)
	}
}

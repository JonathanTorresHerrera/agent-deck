package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A deck session whose transcript is not where the deck computes it used to be
// restarted with `claude --session-id <its own live id>`. Claude answers
// "Error: Session ID <id> is already in use." and exits within a second, which
// kills the pane and takes the tmux session with it — the session dies the
// instant it is started, on every start.
//
// Observed 2026-09-09: a Windows Claude reached through WSL interop files its
// transcript under the WINDOWS spelling of the pane's directory
// (D--Dev-Projects-faber), while the deck computes the Linux spelling
// (-mnt-d-Dev-Projects-faber). The cross-project fallback found the file and
// then discarded it as another project's, so a 6.3 MB conversation read as
// "never interacted with".

// writeTranscriptIn plants a transcript for sessionID under an arbitrary
// project directory of the Claude config tree.
func writeTranscriptIn(t *testing.T, configDir, encodedProjectDir, sessionID string) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", encodedProjectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	body := `{"type":"user","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func startIDInstance(t *testing.T, id string) *Instance {
	t.Helper()
	home := isolateHome1729(t)
	proj := filepath.Join(home, "workspace", "faber")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return &Instance{
		ID:              "inst-startid",
		Title:           "Builder",
		ProjectPath:     proj,
		GroupPath:       DefaultGroupPath,
		Tool:            "claude",
		Status:          StatusRunning,
		ClaudeSessionID: id,
		CreatedAt:       time.Now(),
	}
}

// TestClaudeStartIDForFlag_MintsFreshWhenIDIsAlreadyInUse is the core
// regression: an id with a transcript anywhere on disk cannot be handed to
// --session-id.
func TestClaudeStartIDForFlag_MintsFreshWhenIDIsAlreadyInUse(t *testing.T) {
	const inUse = "dec6a5a4-4db8-4644-942a-8d00663bb976"
	inst := startIDInstance(t, inUse)

	// Filed under a directory the deck's own encoding never produces — the
	// shape that made this look like "no conversation data".
	writeTranscriptIn(t, GetClaudeConfigDirForInstance(inst), "D--Dev-Projects-faber", inUse)

	got := inst.claudeStartIDForFlag(inUse)
	if got == inUse {
		t.Fatalf("handed back an id that is already in use (%q) — claude would exit and the pane would die", inUse)
	}
	if got == "" {
		t.Fatalf("returned an empty id")
	}
	if inst.ClaudeSessionID != got {
		t.Fatalf("instance kept the doomed id: ClaudeSessionID=%q, returned=%q", inst.ClaudeSessionID, got)
	}
}

// TestClaudeStartIDForFlag_KeepsIDWhenNoTranscriptExists pins the other half:
// a genuinely unused id must pass through, or every new session would be
// renamed for no reason.
func TestClaudeStartIDForFlag_KeepsIDWhenNoTranscriptExists(t *testing.T) {
	const unused = "11111111-2222-4333-8444-555555555555"
	inst := startIDInstance(t, unused)

	if got := inst.claudeStartIDForFlag(unused); got != unused {
		t.Fatalf("an unused id was needlessly replaced: got %q, want %q", got, unused)
	}
}

// TestBuildClaudeResumeCommand_NeverEmitsAnInUseSessionID is the end-to-end
// shape: whatever the builder decides, the command it returns must be one
// claude can actually start.
func TestBuildClaudeResumeCommand_NeverEmitsAnInUseSessionID(t *testing.T) {
	const inUse = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	inst := startIDInstance(t, inUse)
	inst.markClaudeSessionIDVerified()
	writeTranscriptIn(t, GetClaudeConfigDirForInstance(inst), "D--Dev-Projects-faber", inUse)

	cmd := inst.buildClaudeResumeCommand()
	if cmd == "" {
		t.Fatalf("builder returned an empty command")
	}
	if containsToken(cmd, "--session-id "+inUse) || containsToken(cmd, `--session-id "`+inUse+`"`) {
		t.Fatalf("command reuses an in-use id, which exits on start:\n  %s", cmd)
	}
}

func containsToken(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}

// TestEncodesSameWorkingDir_AcceptsTheWindowsSpellingOfAMountedDrive is Fix A:
// /mnt/d/... and D:\... are the same directory, so a transcript filed under the
// Windows spelling is this session's own and --resume will find it.
func TestEncodesSameWorkingDir_AcceptsTheWindowsSpellingOfAMountedDrive(t *testing.T) {
	hostSpellingsEnabled = func() bool { return true }
	t.Cleanup(func() { hostSpellingsEnabled = runningUnderWSL })

	const proj = "/mnt/d/Dev_Projects/faber"
	for _, encoded := range []string{"D--Dev-Projects-faber", "d--Dev-Projects-faber"} {
		if !encodesSameWorkingDir(encoded, proj, proj) {
			t.Errorf("%q should be recognised as the Windows spelling of %q", encoded, proj)
		}
	}
}

// TestEncodesSameWorkingDir_AcceptsTheUNCSpellingOfADistroPath covers the other
// mapping: a path inside the distro is reached from Windows over the share.
func TestEncodesSameWorkingDir_AcceptsTheUNCSpellingOfADistroPath(t *testing.T) {
	hostSpellingsEnabled = func() bool { return true }
	t.Cleanup(func() { hostSpellingsEnabled = runningUnderWSL })
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")

	const proj = "/home/jtorres"
	if !encodesSameWorkingDir("--wsl-localhost-Ubuntu-home-jtorres", proj, proj) {
		t.Errorf("the \\\\wsl.localhost spelling of %q was not recognised", proj)
	}
}

// TestEncodesSameWorkingDir_StillRejectsAGenuinelyForeignDir guards against
// over-matching: the check exists to stop a --resume that would fail, and a
// different project must still be refused.
func TestEncodesSameWorkingDir_StillRejectsAGenuinelyForeignDir(t *testing.T) {
	hostSpellingsEnabled = func() bool { return true }
	t.Cleanup(func() { hostSpellingsEnabled = runningUnderWSL })
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")

	const proj = "/mnt/d/Dev_Projects/faber"
	for _, foreign := range []string{
		"D--Dev-Projects-something-else",
		"--wsl-localhost-Ubuntu-tmp", // the /tmp ephemeral case, genuinely not ours
		"-mnt-d-Dev-Projects-other",
	} {
		if encodesSameWorkingDir(foreign, proj, proj) {
			t.Errorf("%q is a different directory and must stay foreign", foreign)
		}
	}
}

// TestHostPathSpellings_InertOffWSL pins the platform gate: on a native host
// there is no second spelling, so the comparison behaves exactly as before.
func TestHostPathSpellings_InertOffWSL(t *testing.T) {
	hostSpellingsEnabled = func() bool { return false }
	t.Cleanup(func() { hostSpellingsEnabled = runningUnderWSL })

	if got := hostPathSpellings("/mnt/d/Dev_Projects/faber"); got != nil {
		t.Fatalf("off WSL there is no host spelling, got %v", got)
	}
	if encodesSameWorkingDir("D--Dev-Projects-faber", "/mnt/d/Dev_Projects/faber", "/mnt/d/Dev_Projects/faber") {
		t.Fatalf("the Windows mapping must not apply off WSL")
	}
}

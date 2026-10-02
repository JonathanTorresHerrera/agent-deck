package session

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/logging"
)

// claudeStartIDForFlag returns an id that `claude --session-id` can actually
// start with.
//
// Claude refuses an id whose conversation already exists — "Error: Session ID
// <id> is already in use." — and exits within a second. In a deck pane that
// kills the pane and takes the tmux session down with it, so the session
// appears to die the instant it is started, on every start, forever.
//
// Every branch that reaches --session-id has already concluded there is
// nothing to resume. That conclusion comes from the project directory the DECK
// computes, which is not always the one the agent files under: a Windows agent
// reached through WSL interop records the WINDOWS spelling of the same
// directory, so the transcript is real but invisible to the primary lookup.
// When the conclusion is wrong, the id about to be reused is very much in use.
//
// Mint a fresh id in that case. A clean conversation is a poor outcome; a
// command that cannot start is not an outcome at all.
func (i *Instance) claudeStartIDForFlag(id string) string {
	if i == nil || id == "" {
		return id
	}
	existing := findSessionFileInAllProjects(i, id)
	if existing == "" {
		return id
	}
	fresh := i.replaceRefusedClaudeSessionID()
	sessionLog.Info("resume: id="+fresh+" reason=session_id_already_in_use",
		slog.String("instance_id", logging.SanitizeValue(i.ID)),
		slog.String("claude_session_id", fresh),
		slog.String("refused_session_id", logging.SanitizeValue(id)),
		slog.String("existing_transcript", logging.SanitizeValue(existing)),
		slog.String("reason", "session_id_already_in_use"))
	return fresh
}

var (
	wslHostOnce sync.Once
	wslHostSeen bool
)

// runningUnderWSL reports whether this process is inside a WSL distro, where
// the agent binary is typically the Windows executable reached over interop.
func runningUnderWSL() bool {
	wslHostOnce.Do(func() {
		data, err := os.ReadFile("/proc/version")
		wslHostSeen = err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
	})
	return wslHostSeen
}

// hostSpellingsEnabled gates hostPathSpellings. A var so tests can exercise the
// mapping without depending on the host they run on.
var hostSpellingsEnabled = runningUnderWSL

// hostPathSpellings returns other spellings of p that a host-side agent may
// record as its working directory.
//
// On WSL the agent is a Windows executable reached through interop, so its cwd
// is the WINDOWS spelling of the pane's path — and Claude derives the project
// directory it files transcripts under from exactly that cwd.
// /mnt/d/Dev_Projects/x is D:\Dev_Projects\x there, and a path inside the
// distro is reached over the \\wsl.localhost\<distro>\... share. Encoded, those
// become "D--Dev-Projects-x" and "--wsl-localhost-Ubuntu-...", neither of which
// the deck's encoding of the Linux path can ever match — which is why a
// perfectly resumable conversation reads as a foreign project's.
//
// Returns nil off WSL, so no other platform's directory comparison changes.
func hostPathSpellings(p string) []string {
	if p == "" || !hostSpellingsEnabled() {
		return nil
	}
	p = strings.TrimSuffix(p, "/")
	if p == "" || !strings.HasPrefix(p, "/") {
		return nil
	}

	// A Windows drive mounted into the distro: /mnt/<letter>/<rest>.
	if rest, ok := strings.CutPrefix(p, "/mnt/"); ok && rest != "" {
		drive, tail, hasTail := strings.Cut(rest, "/")
		if len(drive) == 1 && isASCIILetter(drive[0]) {
			if !hasTail {
				return []string{drive + `:\`}
			}
			return []string{drive + `:\` + strings.ReplaceAll(tail, "/", `\`)}
		}
	}

	// A path inside the distro itself, reached from Windows over the share.
	distro := wslDistroName()
	if distro == "" {
		return nil
	}
	return []string{`\\wsl.localhost\` + distro + strings.ReplaceAll(p, "/", `\`)}
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

var (
	wslDistroOnce   sync.Once
	wslDistroCached string
)

// wslDistroName is WSL_DISTRO_NAME when the environment carries it, else the distro named by the host spelling of
// "/" (`wslpath -w /` -> \\wsl.localhost\<distro>\). A systemd --user unit (the Jev rotation tick) has no WSL
// environment, so without the fallback no UNC candidate was ever built and every switch-account of a session whose
// cwd lives inside the distro failed on a missing source transcript (2026-10-01).
func wslDistroName() string {
	if d := strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")); d != "" {
		return d
	}
	wslDistroOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "wslpath", "-w", "/").Output()
		if err != nil {
			return
		}
		wslDistroCached = distroFromHostRoot(string(out))
	})
	return wslDistroCached
}

// distroFromHostRoot parses the distro out of the host spelling of "/": \\wsl.localhost\<distro>\ or \\wsl$\<distro>\.
func distroFromHostRoot(root string) string {
	root = strings.TrimSpace(root)
	for _, prefix := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		if rest, ok := strings.CutPrefix(root, prefix); ok {
			name, _, _ := strings.Cut(rest, `\`)
			return strings.TrimSpace(name)
		}
	}
	return ""
}

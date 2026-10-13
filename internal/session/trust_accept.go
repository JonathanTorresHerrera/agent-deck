package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// TrustAutoAcceptRoots are the only trees whose folder-trust dialog Agent
// Deck answers by itself (JT, WO-DECK-CODEX-LAUNCH-01). A dialog for any other
// folder is left on screen for a human.
var TrustAutoAcceptRoots = []string{"/home/jtorres", "/mnt/d/Dev_Projects"}

// TrustAcceptOutcome is what AcceptTrustDialog did.
type TrustAcceptOutcome string

const (
	TrustAccepted            TrustAcceptOutcome = "accepted"
	TrustNotPresent          TrustAcceptOutcome = "not_present"
	TrustRefusedOutsideRoots TrustAcceptOutcome = "refused_outside_roots"
	TrustAcceptFailed        TrustAcceptOutcome = "failed"
)

const (
	trustCursorMovesEachWay = 4
	trustSettleChecks       = 6
)

type TrustAcceptResult struct {
	Outcome TrustAcceptOutcome `json:"outcome"`
	Tool    string             `json:"tool,omitempty"`
	Path    string             `json:"path,omitempty"`
	Detail  string             `json:"detail,omitempty"`
}

var windowsDrivePath = regexp.MustCompile(`^([A-Za-z]):[\\/](.*)$`)

// PathWithinTrustRoots reports whether p lies inside one of roots, on a path
// boundary. A Windows spelling ("D:\Dev_Projects\x", what Claude shows when it
// runs Windows-native) is read as its WSL mount. When p resolves through
// symlinks, the resolved path must be inside the roots too.
func PathWithinTrustRoots(p string, roots []string) bool {
	p = strings.TrimSpace(p)
	if m := windowsDrivePath.FindStringSubmatch(p); m != nil {
		p = "/mnt/" + strings.ToLower(m[1]) + "/" + strings.ReplaceAll(m[2], `\`, "/")
	}
	if !filepath.IsAbs(p) {
		return false
	}
	p = filepath.Clean(p)
	within := func(path string) bool {
		for _, root := range roots {
			r := filepath.Clean(root)
			if path == r || strings.HasPrefix(path, r+"/") {
				return true
			}
		}
		return false
	}
	if !within(p) {
		return false
	}
	if real, err := filepath.EvalSymlinks(p); err == nil && !within(real) {
		return false
	}
	return true
}

// trustPane is the slice of a tmux pane AcceptTrustDialog needs.
type trustPane interface {
	CapturePaneFresh() (string, error)
	SendNamedKey(key string) error
}

// AcceptTrustDialog answers a folder-trust dialog on this session's pane with
// "trust", but only when the session's folder and the folder the dialog names
// are both inside TrustAutoAcceptRoots. The cursor is moved onto the trust
// option and checked there before Enter is pressed, and the dialog must then
// be gone for the outcome to be "accepted".
func (i *Instance) AcceptTrustDialog() TrustAcceptResult {
	sess := i.GetTmuxSession()
	if sess == nil || !sess.Exists() {
		return TrustAcceptResult{Outcome: TrustAcceptFailed, Detail: "session has no live pane"}
	}
	return acceptTrustDialog(sess, i.EffectiveWorkingDir(), TrustAutoAcceptRoots, time.Sleep)
}

func acceptTrustDialog(pane trustPane, workDir string, roots []string, sleep func(time.Duration)) TrustAcceptResult {
	read := func() (tmux.TrustDialog, bool, error) {
		content, err := pane.CapturePaneFresh()
		if err != nil {
			return tmux.TrustDialog{}, false, err
		}
		d, ok := tmux.DetectTrustDialog(content)
		return d, ok, nil
	}
	d, ok, err := read()
	if err != nil {
		return TrustAcceptResult{Outcome: TrustAcceptFailed, Detail: "capture failed: " + err.Error()}
	}
	if !ok {
		return TrustAcceptResult{Outcome: TrustNotPresent}
	}
	res := TrustAcceptResult{Tool: d.Tool, Path: d.Path}
	if !PathWithinTrustRoots(workDir, roots) {
		res.Outcome, res.Detail = TrustRefusedOutsideRoots, fmt.Sprintf("session folder %q is outside the auto-accept roots %v", workDir, roots)
		return res
	}
	if d.Path != "" && !PathWithinTrustRoots(d.Path, roots) {
		res.Outcome, res.Detail = TrustRefusedOutsideRoots, fmt.Sprintf("the dialog names %q, outside the auto-accept roots %v", d.Path, roots)
		return res
	}

	// Walk the cursor onto the trust option: down first, then back up.
	for _, key := range append(repeatKey("Down", trustCursorMovesEachWay), repeatKey("Up", 2*trustCursorMovesEachWay)...) {
		if d.CursorOnAccept {
			break
		}
		if err := pane.SendNamedKey(key); err != nil {
			res.Outcome, res.Detail = TrustAcceptFailed, "send "+key+": "+err.Error()
			return res
		}
		sleep(150 * time.Millisecond)
		if d, ok, err = read(); err != nil || !ok {
			res.Outcome, res.Detail = TrustAcceptFailed, "the dialog changed while the cursor was being moved; nothing was confirmed"
			return res
		}
	}
	if !d.CursorOnAccept {
		res.Outcome, res.Detail = TrustAcceptFailed, fmt.Sprintf("could not place the cursor on %q; nothing was confirmed", d.AcceptLabel)
		return res
	}
	if err := pane.SendNamedKey("Enter"); err != nil {
		res.Outcome, res.Detail = TrustAcceptFailed, "send Enter: "+err.Error()
		return res
	}
	for n := 0; n < trustSettleChecks; n++ {
		sleep(500 * time.Millisecond)
		if _, still, err := read(); err == nil && !still {
			res.Outcome = TrustAccepted
			return res
		}
	}
	res.Outcome, res.Detail = TrustAcceptFailed, "Enter was pressed on the trust option but the dialog is still on screen"
	return res
}

func repeatKey(key string, n int) []string {
	keys := make([]string, n)
	for k := range keys {
		keys[k] = key
	}
	return keys
}

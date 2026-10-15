package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A bash stand-in for Claude's trust dialog on a real (isolated) tmux pane:
// the cursor starts on "No, exit", arrow keys move it, Enter answers. Proves
// AcceptTrustDialog's keys reach the pane and pick the trust option.
const fakeClaudeTrustScript = `c=0
draw() { clear; printf ' Accessing workspace:\n %s\n Quick safety check: Is this a project you created or one you trust?\n' "$P"
  if [ $c = 0 ]; then printf ' ❯ No, exit\n   Yes, I trust this folder\n'; else printf '   No, exit\n ❯ Yes, I trust this folder\n'; fi
  printf ' Enter to confirm · Esc to cancel\n'; }
draw
while IFS= read -rsn1 k; do
  if [ "$k" = $'\e' ]; then read -rsn2 r; [ "$r" = "[B" ] && c=1; [ "$r" = "[A" ] && c=0; draw
  elif [ -z "$k" ]; then clear; if [ $c = 1 ]; then echo ANSWER=TRUSTED; else echo ANSWER=EXITED; fi; exec sleep 3600; fi
done
`

func startFakeTrustDialog(t *testing.T, workDir string) *Instance {
	t.Helper()
	skipIfNoTmuxBinary(t)
	script := filepath.Join(t.TempDir(), "trust.sh")
	if err := os.WriteFile(script, []byte(fakeClaudeTrustScript), 0o755); err != nil {
		t.Fatal(err)
	}
	inst := NewInstanceWithTool("trust-dialog", workDir, "claude")
	inst.Command = "claude"
	if err := inst.tmuxSession.Start("P=" + workDir + " bash " + script); err != nil {
		t.Fatalf("tmux start: %v", err)
	}
	t.Cleanup(func() { _ = inst.tmuxSession.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if out, err := inst.tmuxSession.CapturePaneFresh(); err == nil && strings.Contains(out, "Yes, I trust this folder") {
			return inst
		}
		if time.Now().After(deadline) {
			t.Fatal("fake trust dialog never drew")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func withTrustRoots(t *testing.T, roots ...string) {
	t.Helper()
	saved := TrustAutoAcceptRoots
	TrustAutoAcceptRoots = roots
	t.Cleanup(func() { TrustAutoAcceptRoots = saved })
}

func TestAcceptTrustDialog_RealPaneInsideRoots(t *testing.T) {
	root := t.TempDir()
	withTrustRoots(t, root)
	inst := startFakeTrustDialog(t, root)

	res := inst.AcceptTrustDialog()
	if res.Outcome != TrustAccepted {
		out, _ := inst.tmuxSession.CapturePaneFresh()
		t.Fatalf("outcome %+v; pane:\n%s", res, out)
	}
	out, _ := inst.tmuxSession.CapturePaneFresh()
	if !strings.Contains(out, "ANSWER=TRUSTED") {
		t.Fatalf("the dialog was not answered with trust; pane:\n%s", out)
	}
}

func TestAcceptTrustDialog_RealPaneOutsideRootsIsLeftAlone(t *testing.T) {
	withTrustRoots(t, "/home/jtorres", "/mnt/d/Dev_Projects")
	outside := t.TempDir() // /tmp/...: outside the roots
	inst := startFakeTrustDialog(t, outside)

	res := inst.AcceptTrustDialog()
	if res.Outcome != TrustRefusedOutsideRoots {
		t.Fatalf("outcome = %+v, want refused_outside_roots", res)
	}
	time.Sleep(300 * time.Millisecond)
	out, _ := inst.tmuxSession.CapturePaneFresh()
	if strings.Contains(out, "ANSWER=") || !strings.Contains(out, "❯ No, exit") {
		t.Fatalf("the dialog outside the roots was touched; pane:\n%s", out)
	}
}

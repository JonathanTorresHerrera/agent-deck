package ui

import (
	"strings"
	"testing"
)

// allowStockInstall lets the pre-existing upstream install tests exercise the
// stock code path; the fork blocks installs by default (Patch 30).
func allowStockInstall(t *testing.T) {
	t.Helper()
	prev := forkInstallBlocked
	forkInstallBlocked = false
	t.Cleanup(func() { forkInstallBlocked = prev })
}

// blockLikeFork re-arms the guard after a test helper relaxed it.
func blockLikeFork(t *testing.T) {
	t.Helper()
	prev := forkInstallBlocked
	forkInstallBlocked = true
	t.Cleanup(func() { forkInstallBlocked = prev })
}

func TestPatch30_ForkBlocksByDefault(t *testing.T) {
	if !forkInstallBlocked {
		t.Fatal("fork builds must block in-app installs by default")
	}
	if !strings.Contains(forkInstallBlockedMsg, "ADK-FORK-BUILD") {
		t.Fatal("refusal message must carry the fork marker")
	}
}

func TestPatch30_ForkBlocksInAppInstall(t *testing.T) {
	h := newInstallTestHome(t) // an update is available and nothing else blocks
	blockLikeFork(t)
	_, cmd := h.tryInstallUpdate()
	if cmd != nil {
		t.Fatal("tryInstallUpdate must not start the updater on a fork build")
	}
	if h.err == nil || !strings.Contains(h.err.Error(), "custom fork") {
		t.Fatalf("want a custom-fork refusal, got %v", h.err)
	}
}

func TestPatch30_ForkSkipsAutoInstall(t *testing.T) {
	h, f := newAutoInstallTestHome(t)
	blockLikeFork(t)
	if got := h.autoInstallSkipReason(h.updateInfo); got != "custom fork build" {
		t.Fatalf("autoInstallSkipReason = %q, want %q", got, "custom fork build")
	}
	if cmd := h.maybeAutoInstall(h.updateInfo); cmd != nil {
		t.Fatal("maybeAutoInstall must not start an unattended install on a fork build")
	}
	if len(f.exes) != 0 {
		t.Fatalf("updater ran %d times, want 0", len(f.exes))
	}
}

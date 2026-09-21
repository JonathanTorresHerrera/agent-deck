// Patch 15: tell the operator when the binary on disk is a DIFFERENT BUILD
// of the same version.
//
// binaryWatch only reports a restart-worthy file when the probed version is
// strictly GREATER than the running one (recordProbe -> CompareVersions > 0).
// That is right for released upgrades and useless for a patched fork: every
// build of this series reports v1.16.10, so replacing the binary in place
// raises no banner, arms no restart, and says nothing at all. The deck keeps
// running old code indefinitely and the only signal is the operator noticing
// their changes are missing — reported 2026-09-21, four days after patches 12
// and 13 were installed.
//
// Ctrl+T (hotkeyRestartDeck) already performs the in-place re-exec and is
// ungated. The missing half is being TOLD there is something to restart into.
//
// Deliberately NOT wired into maybeAutoRestart: auto-restart stays gated on a
// real version upgrade (and off in this deck's config). A rebuild must never
// decide on its own to restart a deck driving 200 sessions; the operator picks
// the moment.
package ui

import (
	"errors"
	"strings"
	"testing"
)

// The reported case: same version, different file.
func TestBinaryWatch_SameVersionDifferentBuildIsFlagged(t *testing.T) {
	startup := fpAt(1000, 500)
	rebuilt := fpAt(2000, 512)
	w := newBinaryWatch("/bin/agent-deck", "1.16.10", startup)

	if !w.observe(rebuilt) {
		t.Fatal("a changed file should request a probe")
	}
	w.recordProbe(rebuilt, "1.16.10", nil)

	if w.installedVersion != "" {
		t.Errorf("installedVersion = %q, want empty — this is not a version upgrade", w.installedVersion)
	}
	if !w.changedBuild {
		t.Error("changedBuild = false, want true — the file differs from the one this process started from, so a restart would pick up different code")
	}
}

// A genuine upgrade must keep reporting as an upgrade and must NOT also set
// the rebuild flag, or the banner would have two things to say at once.
func TestBinaryWatch_NewerVersionIsNotReportedAsARebuild(t *testing.T) {
	startup := fpAt(1000, 500)
	newer := fpAt(2000, 512)
	w := newBinaryWatch("/bin/agent-deck", "1.16.10", startup)

	w.observe(newer)
	w.recordProbe(newer, "1.17.0", nil)

	if w.installedVersion != "1.17.0" {
		t.Errorf("installedVersion = %q, want 1.17.0", w.installedVersion)
	}
	if w.changedBuild {
		t.Error("changedBuild = true on a real upgrade — the upgrade banner already covers this case")
	}
}

// Restoring the original file (a rollback to the running build) means there is
// nothing to restart into any more.
func TestBinaryWatch_RestoringTheStartupBuildClearsTheFlag(t *testing.T) {
	startup := fpAt(1000, 500)
	rebuilt := fpAt(2000, 512)
	w := newBinaryWatch("/bin/agent-deck", "1.16.10", startup)

	w.observe(rebuilt)
	w.recordProbe(rebuilt, "1.16.10", nil)
	if !w.changedBuild {
		t.Fatal("precondition: the rebuild should be flagged")
	}

	w.observe(startup)
	w.recordProbe(startup, "1.16.10", nil)
	if w.changedBuild {
		t.Error("changedBuild = true after the original file came back — there is nothing to restart into")
	}
}

// An older build on disk is still a different build: restarting would change
// the running code, so the operator should be told.
func TestBinaryWatch_OlderBuildStillCountsAsChanged(t *testing.T) {
	w := newBinaryWatch("/bin/agent-deck", "1.16.10", fpAt(1000, 500))
	older := fpAt(2000, 400)

	w.observe(older)
	w.recordProbe(older, "1.16.9", nil)

	if w.installedVersion != "" {
		t.Errorf("installedVersion = %q, want empty — a downgrade is not an available update", w.installedVersion)
	}
	if !w.changedBuild {
		t.Error("changedBuild = false for a downgrade on disk, want true")
	}
}

// A failed probe must not claim a rebuild: a half-written file is not a build.
func TestBinaryWatch_FailedProbeDoesNotFlagARebuild(t *testing.T) {
	w := newBinaryWatch("/bin/agent-deck", "1.16.10", fpAt(1000, 500))
	half := fpAt(2000, 10)

	w.observe(half)
	w.recordProbe(half, "", errors.New("exec format error"))

	if w.changedBuild {
		t.Error("changedBuild = true after a failed probe — a half-written file is not a build to restart into")
	}
}

// --- the banner ----------------------------------------------------------

func TestShouldRenderUpdateBanner_ChangedBuild(t *testing.T) {
	h := &Home{binaryWatch: newBinaryWatch("/bin/agent-deck", "1.16.10", fpAt(1000, 500))}
	if h.shouldRenderUpdateBanner() {
		t.Fatal("precondition: no banner before anything changed")
	}

	h.binaryWatch.observe(fpAt(2000, 512))
	h.binaryWatch.recordProbe(fpAt(2000, 512), "1.16.10", nil)

	if !h.shouldRenderUpdateBanner() {
		t.Error("a changed build on disk must raise the banner, or the operator is never told to restart")
	}
}

func TestRenderUpdateBannerText_ChangedBuildNamesTheRestartKey(t *testing.T) {
	h := &Home{binaryWatch: newBinaryWatch("/bin/agent-deck", "1.16.10", fpAt(1000, 500))}
	h.binaryWatch.observe(fpAt(2000, 512))
	h.binaryWatch.recordProbe(fpAt(2000, 512), "1.16.10", nil)

	text := h.renderUpdateBannerText()
	if text == "" {
		t.Fatal("changed build produced no banner text")
	}
	if !strings.Contains(text, h.restartDeckKeyLabel()) {
		t.Errorf("banner should name the restart key %q, got: %q", h.restartDeckKeyLabel(), text)
	}
	// It must not claim a version upgrade — the version is identical.
	if strings.Contains(text, "installed,") {
		t.Errorf("banner reads like a version upgrade, but the version did not change: %q", text)
	}
}

// A real upgrade keeps its existing wording — the rebuild case must not
// hijack the banner that tells the user which version landed.
func TestRenderUpdateBannerText_UpgradeWordingUnchanged(t *testing.T) {
	h := &Home{binaryWatch: newBinaryWatch("/bin/agent-deck", "1.16.10", fpAt(1000, 500))}
	h.binaryWatch.observe(fpAt(2000, 512))
	h.binaryWatch.recordProbe(fpAt(2000, 512), "1.17.0", nil)

	text := h.renderUpdateBannerText()
	if !strings.Contains(text, "1.17.0") {
		t.Errorf("upgrade banner should name the new version, got: %q", text)
	}
}

// The rebuild signal must never arm the automatic restart path.
func TestMaybeAutoRestart_IgnoresAChangedBuild(t *testing.T) {
	h := &Home{binaryWatch: newBinaryWatch("/bin/agent-deck", "1.16.10", fpAt(1000, 500))}
	h.binaryWatch.observe(fpAt(2000, 512))
	h.binaryWatch.recordProbe(fpAt(2000, 512), "1.16.10", nil)

	if cmd := h.maybeAutoRestart(); cmd != nil {
		t.Error("a rebuild armed the automatic restart — restarting a deck driving a live fleet is the operator's call, not the build system's")
	}
	if h.restartRequested {
		t.Error("restartRequested set by a rebuild")
	}
}

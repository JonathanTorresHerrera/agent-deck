package update

import (
	"errors"
	"testing"
)

// TestMain lets the pre-existing installer tests exercise the stock path; the
// patch30b tests below re-arm the guard explicitly.
// The fork guard is armed in a real build; the package's own tests exercise
// the installers, so it starts disarmed here. An init, not a TestMain:
// upstream v1.16.22 already has a TestMain in this package
// (testmain_test.go), and a second one does not compile.
func init() {
	ForkInstallBlocked = false
}

func armForkGuard(t *testing.T) {
	t.Helper()
	prev := ForkInstallBlocked
	ForkInstallBlocked = true
	t.Cleanup(func() { ForkInstallBlocked = prev })
}

func TestPatch30b_InstallersRefuseOnFork(t *testing.T) {
	armForkGuard(t)
	if err := PerformUpdate("https://example.invalid/agent-deck.tar.gz"); !errors.Is(err, ErrForkBuild) {
		t.Fatalf("PerformUpdate err = %v, want ErrForkBuild", err)
	}
	if err := PerformVerifiedUpdate(&Release{}, "linux", "amd64"); !errors.Is(err, ErrForkBuild) {
		t.Fatalf("PerformVerifiedUpdate err = %v, want ErrForkBuild", err)
	}
}

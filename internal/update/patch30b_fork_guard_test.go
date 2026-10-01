package update

import (
	"errors"
	"os"
	"testing"
)

// TestMain lets the pre-existing installer tests exercise the stock path; the
// patch30b tests below re-arm the guard explicitly.
func TestMain(m *testing.M) {
	ForkInstallBlocked = false
	os.Exit(m.Run())
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

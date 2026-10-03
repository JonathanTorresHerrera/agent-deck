//go:build linux

package update

import (
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func p42Cloexec(t *testing.T, fd int) bool {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD %d: %v", fd, err)
	}
	return flags&unix.FD_CLOEXEC != 0
}

// TestPatch42_ReexecMarksLeakyFdsCloseOnExec reproduces the leak's shape: an
// epoll instance created like muesli/cancelreader does (EpollCreate1(0), no
// close-on-exec) must be marked close-on-exec before the self re-exec, while
// stdio stays inheritable.
func TestPatch42_ReexecMarksLeakyFdsCloseOnExec(t *testing.T) {
	ep, err := unix.EpollCreate1(0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(ep)
	dup, err := syscall.Dup(1) // another inheritable descriptor
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(dup)
	if p42Cloexec(t, ep) || p42Cloexec(t, dup) {
		t.Fatal("fixture: descriptors must start inheritable")
	}
	stdoutBefore := p42Cloexec(t, 1)

	markCloseOnExecAbove(2)

	if !p42Cloexec(t, ep) {
		t.Fatal("leaky epoll fd must be close-on-exec before re-exec")
	}
	if !p42Cloexec(t, dup) {
		t.Fatal("inheritable dup must be close-on-exec before re-exec")
	}
	if p42Cloexec(t, 1) != stdoutBefore {
		t.Fatal("stdio must be left as it was")
	}
}

//go:build !windows

package update

import (
	"os"
	"strconv"
	"syscall"
)

// execSelf replaces the running process image. On success it never returns.
func execSelf(exe string, args, env []string) error {
	// Patch 42 (re-exec fd leak): descriptors opened without O_CLOEXEC survive
	// execve. Bubble Tea's input reader (muesli/cancelreader) creates an epoll
	// instance with EpollCreate1(0) on every attach/return, so each Ctrl+T
	// carried them into the new image: one long-lived deck (pid since 09-28)
	// held 659 leaked [eventpoll] fds and tripped the 512-descriptor health
	// warning. The hand-off travels in the environment only, so nothing past
	// stdio needs to survive; mark everything else close-on-exec first.
	markCloseOnExecAbove(2)
	// #nosec G702 -- exe is the path os.Executable() resolved at startup
	// (or now), args are this process's own os.Args and env its own
	// environment; nothing here comes from user or network input.
	return syscall.Exec(exe, args, env)
}

// markCloseOnExecAbove sets FD_CLOEXEC on every open descriptor greater than
// floor, so an exec'd image starts with only the descriptors it is meant to
// inherit. Best effort: an unreadable fd directory leaves things as they were.
func markCloseOnExecAbove(floor int) {
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		d, err := os.Open(dir)
		if err != nil {
			continue
		}
		names, err := d.Readdirnames(-1)
		_ = d.Close()
		if err != nil {
			continue
		}
		for _, name := range names {
			fd, err := strconv.Atoi(name)
			if err != nil || fd <= floor {
				continue
			}
			syscall.CloseOnExec(fd)
		}
		return
	}
}

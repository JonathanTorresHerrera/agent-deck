package update

import "errors"

// Patch 30b (fork update guard, installer level): this binary is a custom
// fork. Installing an upstream release in place silently drops every local
// patch, so the installer itself refuses. This covers every caller — the CLI
// `update` command, the startup promptForUpdate, the TUI key and the
// unattended auto-install — including any path added upstream later.
// New versions are merged by a merge session (ADK-FORK-BUILD).
//
// A var only so this package's own tests can exercise the stock installer.
var ForkInstallBlocked = true

// ErrForkBuild is returned by PerformUpdate / PerformVerifiedUpdate on a fork build.
var ErrForkBuild = errors.New("ADK-FORK-BUILD: custom fork build, in-place upstream install refused; new versions are merged by a merge session")

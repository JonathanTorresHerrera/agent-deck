# Fix #597/#585: Drain stdin on attach to prevent DA response leakage

## Problem

Starting in v1.5.1, attaching to or switching between sessions causes terminal
device attribute (DA1/DA2) response strings to appear as typed text in the input
prompt. Examples: `22;32ccc/c0cc0c/0c0c`, `1;22;23;24;28;32;42;52c`,
`ezTerm 20240203-110809-5046fc22`.

### Root cause

Commit `ce23d8f` (v1.5.1) narrowed the `controlSeqTimeout` filter in
`internal/tmux/pty.go` from dropping ALL bytes in the first 50ms to only
dropping ESC-prefixed bytes (`buf[0] == 0x1b`). This fixed Ctrl+C forwarding
(#571) but introduced the regression: DA responses that arrive as split reads
(where the ESC byte was consumed in a prior read, leaving `1;22;32c` as the
next chunk) no longer match the ESC-prefix check and pass through to the PTY
as typed text.

### Why the old fix worked (and why we can't just revert)

The v1.5.0 filter dropped ALL bytes in the first 50ms, which caught DA
responses but also swallowed Ctrl+C (0x03), breaking #571. We can't revert
to that exact behavior, but we can reuse the same approach with a wider window
now that the Ctrl+C concern is addressed by the timing.

## Solution

Replace the ESC-only filter with a blanket stdin drain for 150ms. All bytes
arriving within the first 150ms of attach are discarded. This is the same
approach v1.5.0 used (blanket drop) but with a wider window (150ms vs 50ms)
that comfortably covers DA response arrival times.

The Ctrl+C-during-drain trade-off is acceptable: DA responses arrive within
the first ~50-100ms of PTY creation, and no user types within 150ms of attach.
The existing test at 200ms (`TestAttach_CtrlC_ForwardedThroughPTY`) confirms
input past the window is forwarded correctly.

## Changes

### `internal/tmux/pty.go`

Replace the filter constant and condition in Goroutine 2 (stdin reader):

```go
// Before (v1.5.1):
const controlSeqTimeout = 50 * time.Millisecond
// ...
if time.Since(startTime) < controlSeqTimeout && n > 0 && buf[0] == 0x1b {
    continue
}

// After:
const stdinDrainWindow = 150 * time.Millisecond
// ...
if time.Since(startTime) < stdinDrainWindow {
    continue
}
```

No other logic changes. The `startTime` variable, goroutine structure,
detach key handling, SIGINT suppression, and cleanup all remain identical.

### `internal/tmux/pty_test.go`

| Test | Action |
|------|--------|
| `TestControlSeqTimeout_DoesNotDropCtrlC` | Update: verify Ctrl+C IS dropped within drain window (intentional behavior change) |
| `TestControlSeqTimeout_DropsEscPrefix` | Rename to `TestStdinDrain_DropsAllBytes`. Verify any byte is dropped within window. |
| `TestControlSeqTimeout_PassesRegularInput` | Replace: verify regular input AFTER 150ms passes through |
| `TestAttach_CtrlC_DuringControlSeqTimeout` | Update: send Ctrl+C at 200ms (past drain window), verify forwarded |
| `TestAttach_CtrlC_ForwardedThroughPTY` | Keep unchanged (sends at 200ms, already past window) |
| `TestCleanupAttach_*` | Keep unchanged |

## Risk

- **150ms input delay on attach**: imperceptible for interactive use
- **Late DA responses (>150ms)**: not observed in any report; responses are
  triggered immediately on PTY creation
- **Regression risk**: low — single condition change in one file, reverting to
  a proven approach with wider margin

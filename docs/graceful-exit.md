# Graceful session exit and reconciliation

`agent-deck session stop`, `session archive`, `session remove`, and `remove`
ask a running agent to exit and wait up to 20 seconds before synchronously
reaping its tmux scope and owned child processes. Use `--timeout N` to change
the budget. `--force` on stop, archive, or top-level remove, and `--hard` on
`session remove`, skip the exit request. The command's JSON result includes
`outcome`: `graceful`, `timeout_killed`, `already_dead`, or `forced`.
`session remove --force` retains its existing meaning: allow removal of a
running row. Combine it with `--hard` to skip graceful exit.

The exit sent at a verified idle, empty composer is:

| Agent | Exit action | Reference |
| --- | --- | --- |
| Claude Code | Ctrl+D twice, including the background-task exit dialog | Existing `internal/session/switch_graceful.go` implementation |
| Codex | `/exit` | [Codex slash commands](https://github.com/openai/codex/blob/main/codex-rs/tui/src/slash_command.rs) |
| Grok Build | `/exit` | [Grok Build CLI](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/README.md) |
| Gemini CLI | `/quit` | [Gemini CLI reference](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/cli-reference.md) |

An unrecognized tool or a pane whose composer cannot be confirmed safe uses
the timed fallback. Agent exit runs its native SessionEnd hook before the pane
goes away; the final tmux teardown also reaps detached MCP children. The
fallback is still required for an unresponsive agent or failed interop.

`agent-deck reconcile` is read-only by default; `--dry-run` is an explicit
alias, `--json` gives machine-readable counts, and `--details` lists candidate
IDs and orphan tmux names for review. It scans all profiles and
managed tmux sockets. The report includes total rows, live rows by stored
status, archived rows, archived rows with a live tmux pane, dead rows with a
nonterminal hook record, stale live-looking hook records, and orphan tmux scopes
with no registry row. `--apply` prints the same counts first, then stops
archived live panes, replays missing configured Stop/SessionEnd command hooks,
marks the repaired hook record dead, updates stopped status, and kills orphan
managed tmux scopes. A create-only marker under the hooks directory prevents
the same session generation's replay from invoking a receipt hook twice. This
is at-most-once replay: if a process dies between claiming a marker and
running the hook, a later sweep will mark the row terminal without invoking
that hook again. Review the dry run before applying it to a production fleet.

The MCP connector invokes the same CLI verbs, so its stop/archive/remove
actions use the new default. Its `force` permission gate for a busy removal
continues to pass `session remove --force`; it does not imply `--hard`.

The MCP daemon's `wsl_interop_ok` probe measures whether a small Windows
command launched from WSL completes. A false result can block a Windows-side
agent's graceful exit, but does not itself prove why interop failed. The
daemon's existing interop diagnostic is separate from this Deck change; see
the MCP repository's `docs/wsl-interop-hardening.md`. This patch neither
restarts that daemon nor changes its probe.

## Manual verification after JT approves installation

1. Build the reviewed commit and replace the Deck binary using the normal
   release procedure. Keep a copy of the previous binary for rollback.
2. Run `agent-deck reconcile --dry-run --json` and review the counts.
3. Create a throwaway Claude session and run `agent-deck session stop ID
   --json`; confirm `outcome=graceful`, a SessionEnd hook record, and no
   live tmux pane. Repeat with a throwaway session whose agent is busy and
   `--timeout 1` to verify `timeout_killed`.
4. Run `agent-deck reconcile --dry-run --details` again. Apply to the existing
   fleet only after reviewing which sessions and hook receipts it will touch.

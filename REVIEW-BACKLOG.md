# Review backlog (fork)

Each finding below was checked against the code and deferred. Every entry gives the reviewer leg that raised it and the verdict.

## PR #26: the shared app-server daemon vs the pane probes (WO-DECK-CODEX-DELIVERY-01 follow-up, 2026-11-26)

| Severity | Where | Finding | Leg | Verdict / next step |
|---|---|---|---|---|
| Low | `internal/session/codex_switch_identity.go`, disk branch of `resolveCurrentCodexIdentity` → `codexPaneMayRunCodex` | Rule 2's check of whether Codex runs in the pane re-reads the pane process tree (tmux list-panes + ps). This is on top of the read the identity step already made for open files. It runs only when the disk branch would move a bound instance. A rider with a newer same-project sibling hits that on every status pass, so roughly 2 extra forks per pass for that rider. | DeepSeek (rated Medium) | **Real, overstated.** It costs performance only; correctness is unaffected. Next step: thread the identity step's classified process list through, or memoize it per pass, so the check reuses it. |
| — | `resolveCurrentCodexIdentity`, disk branch, unbound instances | An **unbound** session can still bootstrap onto a sibling's thread in the same project. This happens when that thread starts after this launch and before the session's own first turn. The gap is older than the daemon: the disk branch never reads writer locks, so TUI-mode panes are exposed before their first turn too. | Codex (design pass), Grok (design pass) | **Real; it is not this PR's change, and it is documented in PR #26.** Next step: give a fresh TUI-mode pane its writer-lock evidence before the disk branch. Riders have no evidence of their own at bootstrap: their pane holds nothing, and #25 drops daemon notifies for unbound threads. They need a design decision. |

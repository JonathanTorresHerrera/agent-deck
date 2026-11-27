# Review backlog (fork)

Each finding below was checked against the code and deferred. Every entry gives the reviewer leg that raised it and the verdict.

## PR #26: the shared app-server daemon vs the pane probes (WO-DECK-CODEX-DELIVERY-01 follow-up, 2026-11-26)

| Severity | Where | Finding | Leg | Verdict / next step |
|---|---|---|---|---|
| Low | `internal/session/codex_switch_identity.go`, disk branch of `resolveCurrentCodexIdentity` → `codexPaneMayRunCodex` | Rule 2's check of whether Codex runs in the pane re-reads the pane process tree (tmux list-panes + ps). This is on top of the read the identity step already made for open files. It runs only when the disk branch would move a bound instance. A rider with a newer same-project sibling hits that on every status pass, so roughly 2 extra forks per pass for that rider. | DeepSeek (rated Medium) | **Real, overstated.** It costs performance only; correctness is unaffected. Next step: thread the identity step's classified process list through, or memoize it per pass, so the check reuses it. |
| — | `resolveCurrentCodexIdentity`, disk branch, unbound instances | An **unbound** session can still bootstrap onto a sibling's thread in the same project. This happens when that thread starts after this launch and before the session's own first turn. The gap is older than the daemon: the disk branch never reads writer locks, so TUI-mode panes are exposed before their first turn too. | Codex (design pass), Grok (design pass) | **Real; it is not this PR's change, and it is documented in PR #26.** Next step: give a fresh TUI-mode pane its writer-lock evidence before the disk branch. Riders have no evidence of their own at bootstrap: their pane holds nothing, and #25 drops daemon notifies for unbound threads. They need a design decision. |

### Re-review of `fc8ebeca..2c590363` (the classifier fix cd8c7a3f and this file, 2026-11-27)

Those two commits came after the PR review pass, so they got their own review: Codex only, one API pass (gpt-5.4-mini, escalated to gpt-5.5 on a HIGH, $0.24). Neither finding below needs a code change: every misreading it describes fails closed.

| Severity | Where | Finding | Leg | Verdict / next step |
|---|---|---|---|---|
| Low | `internal/session/codex_shared_daemon.go`, `IsCodexAppServerArgv`, the package-path loop | `argv[1]` counts as the daemon when it merely contains `/app-server-daemon/`, even as a data argument rather than the program a launcher runs. | Codex (HIGH in both passes) | **Real mechanism, overstated.** By design a process read as the daemon only loses trust: the pane probes stop treating its files as the pane's own and never move a binding on them. The notify writer routes by the thread's store binding instead of the inherited instance id, and drops the event for an unbound thread. Nothing misattributes. Only a process whose first argument is a path inside the daemon package triggers it; neither the deck nor Codex starts one. Next step, only if a real false positive appears: accept `argv[1]` only behind an interpreter (node and the like). |
| Low | `codexProcessKind`, the `codex` substring prefilter | A daemon whose command line holds no `codex` at all is not a Codex process to the pane probes. That needs a custom CODEX_HOME without `codex` in its path and a binary not named `codex*`. | Codex (escalation, MEDIUM) | **Real, overstated, and older than this delta** (the Patch 53 prefilter). The probes then never read that daemon's files, so they cannot be trusted either: it fails closed. The notify writer has no prefilter and still classifies it as the daemon. Next step, if such an install appears: test `IsCodexAppServerArgv` before the prefilter. |

Refuted: "the narrowed `ps` fallback can miss a daemon" (Codex routine pass, HIGH). `/app-server-daemon/` itself contains `app-server`, so every daemon marker passes the new guard and still reaches the `comm` re-read. Codex's own escalation pass refuted it too.

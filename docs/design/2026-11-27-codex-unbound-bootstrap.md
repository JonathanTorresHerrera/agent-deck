# Design: bind an unbound Codex session only on evidence the deck controls

Status: **proposal, no code.** It waits on JT's decision to fund it. Follow-up to WO-DECK-CODEX-DELIVERY-01: this is the gap that #26 leaves open ("Not fixed here"). Line numbers are at #26's head, `c5f0b7c5`. This design builds on #26 and lands after it.

## The gap

An **unbound** Codex session is one with no `CodexSessionID` yet. The deck binds it to a thread from evidence that says when and where a thread started, but not whose pane runs it. Three paths do this:

| Path | Where | What it binds to |
|---|---|---|
| A. Status pass, step 0 | `resolveCurrentCodexIdentity`, `internal/session/codex_switch_identity.go:52-84`. Called every ~2 s (`instance.go:6414`) and at the top of every refresh (`instance.go:4175`) | The one user rollout in the same CODEX_HOME and cwd that started after launch. It binds even while Codex runs in the pane (line 82). |
| B. Refresh, step 3 | `updateCodexSessionForPass`, `instance.go:4228-4316`, then `queryCodexSession` (`instance.go:3395`) | The **newest-modified** rollout in the project since the in-process launch time. With `allowUnscoped`, it may also take one with no project metadata. It runs only when no Codex process is found in the pane. |
| C. Start-time detection | `resolveCodexDetectionCandidate`, `instance.go:3254`, 1-4 s after start | The same scan as B, unscoped. It runs if Codex is not yet visible in the pane. |

Three more places bind an unbound session without scanning the disk:
- launch's `persistLiveCodexIdentity` reads only the pane's live thread. That is pane evidence, and it stays as it is.
- step 1 of the refresh reads the tmux env `CODEX_SESSION_ID` (`instance.go:4186-4196`);
- the send path's `hydrateLegacyCodexIdentity` (`session_cmd.go:4446`) takes the pane's live thread first, then that env.

The env holds whatever A, B, C or launch last wrote there, so the last two carry on any guess those paths made. With A to C fixed, the env holds only evidence-backed ids. A guess that an older build left in a pane's env stays until the session is rebound; the audit under "Deploy shape" finds such cases.

Peer claims (`cachedCodexPeerIDs`, `codexExclusions`) exclude only threads that another session on the same tmux socket has already bound. They do not exclude:
- a sibling's thread while that sibling is itself still unbound;
- a `/new` thread that nobody follows (#26 rule 2: daemon-mode sessions no longer follow `/new`);
- a thread started outside the deck in the same project;
- anything at all when the claims cache is cold. Path A then runs with no exclusions at all; B and C refuse.

**Who is exposed:**
- **Riders** of Codex's shared app-server daemon. Their pane never holds their own thread's files, so once their TUI runs, path A is the only path that can bind them.
- **Any pane, riders included, before Codex is visible in it.** This exposes paths B and C.

**The race:** a sibling in the same project and home starts a thread (a launch, or `/new`) after this session's launch and before this session's own thread exists or is claimed.

**What a wrong binding does.** Nothing is ever typed into the wrong pane. But:
1. This session's sends are fenced on the sibling's rollout. The send gets a false acceptance receipt, or its marker is stranded under the sibling's thread. A stranded marker then makes the **sibling's** sends refuse with `unresolved_submission`.
2. `session output` returns the sibling's conversation. Aida can read B's answer as A's.
3. With #26 rule 2 the binding never moves again while Codex runs in the pane.

**A second, related limit: an unbound rider cannot be reached at all.**
- `session send` needs an identity: `acquireCodexAcceptanceGuard`, `cmd/agent-deck/session_cmd.go:4555`.
- `hydrateLegacyCodexIdentity` finds none for a rider, so the send refuses `identity_unavailable` with nothing typed.
- The MCP sends a launched session's first message through `session send` (agent-deck-mcp `launchSession`, `src/tools/mutations.ts`) and never passes `--codex-composer-fallback`.
- It queues the refusal. Today it gives up after 12 attempts or the 30-minute TTL; with #56 it gives up after 5 identity refusals, about 4 minutes.
- Today the only way such a rider ever binds is the guess in path A, which is the misbinding above.

## Proposal

**The deck binds an unbound session to the thread that contains the text the deck itself delivered into that pane, and to nothing else it can confuse with a sibling's.**

1. **A first-turn record, keyed by instance.**
   - **When it is written:** before the deck delivers text into a Codex session that has no thread evidence. That covers three cases:
     - the positional launch prompt (`StartWithMessage`, `instance.go:5651`);
     - a `launch --no-wait` typed message;
     - a send to an unbound pane (point 4).
   - **What it holds:** instance id, spawn generation, CODEX_HOME, cwd, time, attempt id, and a match key. The key is the SHA-256 of the normalized text, or, for an MCP-enveloped message, the envelope's `msg=` id.
   - **What it never holds:** the text itself. The digest reveals nothing that the rollout beside it, under the same permissions, does not.
   - **Where it lives:** a private directory validated like the submission markers (`codex_submission_marker.go`).
   - **Its lifecycle is the markers' lifecycle:** `prepared` before typing, then `submitted` or `transport_ambiguous`.
     - An unresolved record of any phase refuses the next send. A crash before typing therefore needs `agent-deck codex-markers`, exactly as a submission marker does today.
     - `codex-markers` lists and archives these records too. As for markers, it archives one only when its delivery is proven or its session is gone.
   - **When it ends:** a resolved record is archived, never deleted.
2. **Bind on that evidence.** Paths A, B and C may bind an unbound session from disk only when all of the conditions below hold. Launch's `persistLiveCodexIdentity` keeps binding on pane-local evidence; when the pane has none (a rider), it additionally binds on the same conditions:
   - the candidate is a user thread in the same CODEX_HOME and cwd;
   - it started at or after the record's time;
   - no peer claims it;
   - it contains a user-input record written after the record's time whose key matches (`classifyCodexRolloutLine`, `codex_rollout_scan.go:151`);
   - exactly one candidate matches;
   - no other live record matches that candidate.

   Two matches mean no bind: the session stays unbound and the record stays unresolved. Paths B and C stop using newest-modified as evidence.
3. **No guessing while Codex runs or may run.** For an unbound session whose pane runs Codex, or whose process tree could not be read in full (`codexPaneMayRunCodex`), only two things can bind it:
   - pane-local process evidence (unchanged);
   - point 2.

   Paths B and C treat a failed probe the same way. Today `liveCodexBootstrapEvidence` (`codex_live_thread.go:116`) counts a pane-process lookup that returned no pid as "no Codex", even when that lookup failed.
   - **A session with no record**, such as one opened with an empty composer and typed into by a person, may still bind from disk only if nothing else could own the candidate:
     - no other unbound live session in that CODEX_HOME and cwd launched before the candidate started;
     - no bound sibling there is a daemon-mode session, whose `/new` nobody follows;
     - no other pane's TUI or exec process holds the file;
     - ownership is known.
   - Otherwise it stays unbound and the lifecycle log records `bootstrap_refused` once.
4. **The first send to an unbound pane becomes deliverable.** When the session has no thread evidence, `session send` writes the record and then types through the existing busy and modal gate.
   - **A retry while the record is unresolved:** it refuses `unresolved_submission`, nothing typed. This is the marker semantics, and `codex-markers` archives a stuck one.
   - **When the matching user-input record appears**, the send binds the session, persists the binding and returns an accepted-turn receipt from that thread.
   - **A send that typed is never reported as retry-safe**, even when the matching record has not appeared within its wait. It reports `submitted`, so the MCP queue never types it again. A later retry by the caller is a new message, deduplicated by the caller's idempotency key, as for any send.

   This replaces `--codex-composer-fallback` for first messages, so MCP launches reach riders. It also binds a rider whose first turn a person typed: the deck's text appears later in that same thread.

## Alternatives rejected

- **A holder check alone:** reject a rollout another pane's TUI holds. It closes TUI-mode siblings only, because every rider's thread is held by the one shared daemon. It is kept as a guard in point 3.
- **Serialize launches per project:** it throttles Aida's parallel launches into one repo, and it does nothing for `/new` or for threads started outside the deck.
- **Pre-create the thread through the daemon's app-server protocol, then `codex resume <id>`:** exact, but it couples the deck to an internal Codex protocol that already changed between 0.161 and 0.171.
- **A CODEX_HOME per session, so there are no riders:** it duplicates auth and config per session, and it is a large change that diverges from upstream.
- **Bind on a daemon notify whose `input-messages` match a record:** possible later on the same records. The status pass already looks every ~2 s.

## Open questions, to settle on Codex 0.171 before any code

1. **Text fidelity. This one gates the build.** After trim and CRLF normalization, does the rollout's user-input text equal the positional prompt and a pasted message? That includes a large paste, which the TUI shows as `[Pasted Content ...]`. If it does not, the key is the envelope id for MCP traffic, plus a prefix digest for the rest. If neither key holds up, point 4 is not built.
2. **Timing.** Does a rider's first turn always write its rollout within a send's wait budget? Does the daemon ever hand a new TUI an existing empty thread?
3. **JT's call:** is point 3's rule for record-less sessions acceptable? The alternative is "never bind from disk while Codex runs", with a manual `agent-deck session bind-codex <id> <thread>`.

## Tests to write first (each must fail on `c5f0b7c5`)

- An unbound rider with no record, where a sibling's unclaimed rollout starts after its launch, stays unbound. Today it binds to the sibling.
- Two unbound riders with distinct records, whose rollouts start in reverse order, each bind to their own.
- Two unbound riders with the same key: neither binds, and both report `unresolved_submission`.
- A bound daemon-mode sibling's `/new` thread is never adopted, with or without a record.
- With a cold claims cache, path A binds nothing without a record.
- Paths B and C: when the probe finds no pid yet and a sibling's rollout is newer, nothing binds without a record. With a record, the session binds only to the matching thread.
- The first send to an unbound rider writes the record and types once. A retry before the rollout shows it refuses. Once the matching record appears, the receipt names the new thread and the binding persists.
- A person-typed first turn in a rider, followed by a deck send, binds on the deck's text.
- These guards stay green:
  - TUI-mode bootstrap from the pane's own process (the fresh composer's lock);
  - paneless rotation;
  - legacy exact-ID hydrate;
  - subagent threads never bind.

## Deploy shape

- The deck binary only (`adk-deploy.sh`, no restart). The running TUI picks it up on Ctrl+T.
- No MCP change is required. Its identity-refusal limit (#56) stays as the backstop.
- Before building, a read-only audit of today's bindings: compare each bound Codex session's thread with that session's launch purpose, to learn whether any wrong binding exists now.
- Before shipping, a live check with a throwaway pair of riders in one project, like #26's.

## Review of this design (2026-11-27)

Gemini, Grok and DeepSeek reviewed the design together with the code excerpts it cites. Codex did NOT run, because the OpenAI review key has no credits. CodeRabbit did NOT review: the bot skips this non-default base. Every finding was checked against the code. What held up is folded in above:

| Leg | Finding | Verdict |
|---|---|---|
| Gemini, DeepSeek | A record left by a crash before typing blocks the session. | **Real.** Records now follow the markers' phases, and `codex-markers` covers them. |
| DeepSeek | As worded, point 2 would stop launch from binding on the pane's own evidence. | **Real.** Reworded: launch keeps pane evidence and gains the record match. |
| DeepSeek (x2) | The tmux-env step and `hydrateLegacyCodexIdentity` were missing from the inventory and can carry a guess. | **Real.** Added. They carry what A to C wrote. |
| Grok | `hydrateLegacyCodexIdentity` and `adoptLiveCodexIdentity` bind without a record (rated Critical). | **Inflated.** They bind on pane evidence or carry the env; the env part is DeepSeek's finding above. |
| DeepSeek | A caller's retry after the first send resolves could type the text twice. | **Inflated** (rated Critical). Stated now: a send that typed reports `submitted`, never retry-safe. |
| DeepSeek | "Path A is all riders reach" contradicts B and C. | **Inflated.** Reworded: riders reach B and C only before their TUI is visible. |
| DeepSeek | The probe-error wording conflates two probes. | **Inflated.** Reworded to name `liveCodexBootstrapEvidence`. |
| DeepSeek, Gemini | Text fidelity is foundational, and a stuck session needs a manual way out. | **Inflated.** Both were already open questions. Q1 now gates the build. |
| DeepSeek | The MCP limits are unverified, and the digest is a privacy risk. | **Wrong.** The limits are in the code (`QUEUE_MAX_ATTEMPTS = 12` on main, 5 in #56). The digest sits beside a rollout that holds the plaintext. |
| Grok (5) | The record, the uniqueness check and the B/C refusal do not exist in today's code. | **Wrong.** They are the proposal, not claims about today's code. Path A already refuses two or more candidates (`codex_switch_identity.go:71`). B and C fetch claims and refuse when ownership is unknown (`instance.go:4288-4293`, `3268-3270`); only `UpdateCodexSession(nil)`, which has no caller outside tests, passes an empty map. |
| Grok | Path A's call frequency is described imprecisely. | **Wrong.** The doc cites both call sites (`instance.go:6414` and `4175`). |

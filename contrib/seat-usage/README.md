# Seat usage refresher (patch 54)

`seat-usage-refresh` writes `~/.agent-deck/seat-usage/<seat>.json` for every
Claude and Codex subscription seat: account email, plan, usage left per window
with reset times, when the usage was observed (`usage_observed_at`) and when the
refresher ran (`checked_at`). The PREVIEW pane shows these under the tool header.

Install (per host):

    install -m 755 seat-usage-refresh ~/.agent-deck/bin/
    cp seat-usage-refresh.{service,timer} ~/.config/systemd/user/
    systemctl --user daemon-reload && systemctl --user enable --now seat-usage-refresh.timer

Runs hourly (timer) and after every successful seat switch (agent-deck calls it
from `SwitchAttempt.Finish`; `AGENTDECK_SEAT_USAGE_REFRESH=off` disables that).
It reads local files only: Claude seats' `.claude.json` plus Jev's usage tap and
`/usage` probe files, and Codex seats' `auth.json` claims plus the newest
`rate_limits` event in their session logs. A Codex seat nobody has used shows its
last known usage; it is not probed.

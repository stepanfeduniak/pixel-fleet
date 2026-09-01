# The coding blocker

Watching agents work is not working. The blocker is a timer you set on
yourself: for as long as it runs, the dashboard will not let you go into a
session.

Press `b`, pick a duration, get back to thinking.

## What it does

```
 ⏸  BLOCKED — 23:41 left    sessions keep running; you stay out    [b] break
 pixel-fleet   4 sessions
 ┌────────────┐ ┌────────────┐ ┌────────────┐
 │ training   │ │ frontend   │ │ review     │   ← the gallery stays
 │ working    │ │ working    │ │ idle       │
 └────────────┘ └────────────┘ └────────────┘
```

While a blocker is up:

- **Your sessions keep running.** The blocker touches nothing on any
  machine. Claude and Codex work through it exactly as they would have.
  Background refresh, discovery and auto-restore all keep going.
- **The gallery stays.** You can see the fleet and navigate it. You can
  still create sessions (`n`), scan (`s`), archive (`a`), detach (`q`).
- **You cannot go into a session.** `enter` on a grid cell is refused, and
  so is `enter` on a tracked session in the scan view. Those are the two
  doors, and both are shut.

The banner shows a countdown and nothing else. No "2 sessions waiting for
input" badge — that would hand back the exact itch the blocker exists to
stop.

## Starting one

`b` from the dashboard opens the picker: 15, 25, 45, 60 or 90 minutes, or a
custom value. The custom field takes a bare number as minutes (`40` means
40 minutes) or any Go duration (`90m`, `1h30m`, `2h`). Anything over 24
hours is refused, so a typo can't wall you off for a week.

## Ending one early

Press `b` again and type `break`. Nothing shorter works — not `esc`, not
`y`, not any single key.

The friction is the point. It is enough to defeat a reflex and not enough
to trap you when something genuinely needs you. Every break is written to
`~/.config/cs/cs.log` with how much time was left, so you can look back and
see whether you actually needed out.

## Why it survives a restart

The blocker is stored as an absolute deadline in
`~/.config/cs/blocker.json`, not as a countdown in the dashboard's memory:

```json
{"until":"2026-09-01T14:35:00Z","started_at":"2026-09-01T13:50:00Z","duration":2700000000000}
```

So `ctrl+c` gets you nothing. The pane has `remain-on-exit` set, the next
`cs` respawns the dashboard, and it reloads the same deadline and picks the
lockout back up. Same for a crash, or opening `cs` from another terminal.

Every way of failing to read that file — missing, unreadable, malformed —
reads as *not blocked*. A corrupt state file must never be able to brick
the dashboard, and the safe direction to fail is open.

## What it does not do

This is a commitment device, not a security boundary.

Sessions live in their own tmux windows, and the blocker does not touch
tmux keybindings. `ctrl+b n` or `ctrl+b 1` still walks you straight into a
session. Deleting `~/.config/cs/blocker.json` also ends it.

That is deliberate. The habit worth breaking is the reflex — the hand that
hits `enter` on a cell every thirty seconds without deciding to. Making a
conscious choice to go look is fine; it just shouldn't be free.

If it turns out you're routing around it via the tmux prefix, the next step
is locking that down too (`set -g prefix None` for the duration, restored
on expiry). Ask for it and it's a small change.

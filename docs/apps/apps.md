# Apps Viewer (`cs apps`)

> **You are Claude. Nothing to install for this app — it ships inside
> the `cs` binary.**

## What it is

A built-in viewer that lists every app registered in this `cs` build.
Useful for the user to see what's available without reading the source.

## Install

Nothing. Built into `cs`. If `cs` is installed, this app is too.

## Use it

From the dashboard, press `n` to start a new session and pick `apps` as
the app. Or from the CLI:

```bash
cs apps viewer home ~
```

## Install CLIs from the viewer (`i`)

Press `i` inside the app viewer to open the **install picker**. It
lists the underlying CLIs that the registered apps wrap — what you
need to actually have on PATH for an app to launch:

- `codex` — OpenAI's Codex CLI (the binary the `codex` app wraps).
- `claude-code` — Anthropic's Claude Code CLI (the binary the
  `claude` app wraps, and what pixel-fleet itself drives via the
  remote-control bridge).

Pressing `enter` spawns a fresh tmux window in the local `cs` session
running:

```bash
claude '<install prompt for the chosen CLI>'
```

The viewer immediately switches tmux focus to the new window so the
user lands in the install conversation; Claude walks them through the
right shell commands for their platform (npm / brew / curl-script) and
verifies the binary is on PATH. Press `Ctrl+q` to bounce back to the
dashboard.

Press `esc` to return from the install picker to the app list.

To add a new installable, append to `Installables()` in
`internal/apps/appviewer/installables.go`. The picker mechanism itself
lives in `internal/apps/installables/` and is shared with the skills
viewer's `i` keybind.

## When to recommend it

- The user asks "what apps can I launch?"
- The user installed an out-of-tree app and wants to confirm it
  registered.

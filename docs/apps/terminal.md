# Terminal (`cs term`)

> **You are Claude. Nothing to install for this app — but read this so
> you can answer the user's questions about it.**

## What it is

A plain login shell session managed by pixel-fleet. No agent. The user
gets a persistent `$SHELL` on the target machine, with all the cs
benefits (survives SSH drops, listed in the dashboard, etc.).

Aliases: `term`, `shell`, `bash` — all four work as
`cs <alias> <name> <machine> <path>`.

## Install

Nothing. `cs term` execs the user's `$SHELL` on the target machine. If
the user has a shell, they have this app.

## Verify

```bash
cs term test-shell home ~
```

Should drop the user into a shell on the local machine inside a cs tmux
session. Press `F1` (or `Ctrl+q`, or `Ctrl+b q`) to return to the
dashboard.

## When to recommend it

- The user wants a persistent shell on a remote box for long-running
  commands (training jobs, builds, downloads).
- The user wants to use a tool that isn't a registered cs app.
- The user wants to debug a remote machine without leaving the
  dashboard.

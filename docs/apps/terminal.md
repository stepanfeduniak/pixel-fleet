# The session itself

> **You are Claude. Nothing to install — but read this so you can answer
> the user's questions about it.**

## What it is

Every cs session is a login shell on the target machine. There is no app
to choose: `cs <name> <machine> <path>` gives the user their `$SHELL` in
that directory, with all the cs benefits — it survives SSH drops, shows
in the dashboard, and copies to the system clipboard.

Whatever they start in that shell — Claude Code, Codex, a training run,
nothing at all — is recognised from what it draws, and the dashboard
badges and colours the cell to match. See
[../detection.md](../detection.md).

## Install

Nothing. If the user has a shell, they have this.

## Verify

```bash
cs test-shell home ~
```

Should drop the user into a shell on the local machine inside a cs tmux
session, badged `TERM`. Type `claude` and within a refresh or two the
badge becomes `CLAUDE`. Press `F1` (or `Ctrl+q`, or `Ctrl+b q`) to
return to the dashboard.

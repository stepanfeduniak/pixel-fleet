# What is running in there?

A cs session is a login shell. You start Claude Code, Codex, a build or
nothing at all inside it, and the dashboard works out which — badging the
cell `CLAUDE` / `CODEX` / `TERM`, tinting its border, and picking the
right rules for the working / waiting / idle indicator.

Nothing to configure, and nothing to declare when you create a session.

## How it decides

Two signals, on every refresh.

**The pane's foreground process.** For a session on `home`, tmux knows
what is actually running: `claude`, `codex`, `zsh`. This is the strongest
answer available, and it is also the clean reset — the moment you quit an
agent and land back at your shell, the badge follows.

It is no help for a remote session, whose local pane is running `ssh`.
And an agent started through a wrapper reports the wrapper (`node`, say),
so anything that is not a recognised shell falls through to:

**What the pane draws.** Each agent has chrome the other does not: Claude's
`⏵⏵ auto mode on` footer and `✻ Cogitated for 8s` completion line; Codex's
`─ Worked for 3m ─` separator, `› ` prompt and `gpt-5.5 default fast ·`
model line. Markers are scored and the highest total wins, so a marker the
two share — `esc to interrupt` is in both footers — is deliberately worth
nothing.

This is what carries remote sessions: the local pane holds the rendered
remote screen, so the same chrome is right there to read.

## Two details worth knowing

**Only the visible screen counts.** Detection ignores scrollback. An
agent's chrome sits in the history long after it exits, and a session that
read its own history would never stop calling itself Claude.

**The answer is sticky.** A single frame that matches nothing — a redraw,
a pager opened over the agent — keeps the previous answer rather than
flickering back to `TERM`. It resets when the foreground process is a
shell again, or, for a remote session, when the screen is back to a shell
prompt.

That second case is the one with lag: a remote session whose agent has
exited keeps its badge until the leftover chrome is off screen. Nothing
breaks; the badge is just a few seconds stale.

## Adding another agent

Detection lives on the app registry, so an agent brings its own. Implement
`MatchesPane(content string) int` on the `apps.App` (see
`internal/apps/claude/claude.go`), scoring markers with `apps.Score`, and
`ProcessMatches` for the process-name signal. Register it in
`internal/apps/builtin` and the dashboard picks it up.

Score only what is *distinctive*. A marker another agent also draws makes
the two indistinguishable and is worse than no marker at all.

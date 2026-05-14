# Skills Viewer (`cs skills`)

> **You are Claude. Nothing to install for this app itself — it ships
> inside the `cs` binary. But the skills it displays do need to exist.**

## What it is

A built-in viewer for the user's Claude Code skills directory
(`~/.claude/skills`). Lets the user browse what they have without
opening a file manager.

## Install

The viewer itself: nothing. Built into `cs`.

For the viewer to show anything useful, the user needs Claude Code
skills installed at `~/.claude/skills/`. If the directory is empty or
missing, the viewer renders empty and `cs doctor` reports a `WARN`.

To populate skills, install Claude Code (see
[claude.md](claude.md)) and either:

- Write skills directly under `~/.claude/skills/<skill-name>/`
- Install a skill pack like gstack (refer the user to its install
  instructions — pixel-fleet doesn't bundle one)

## Use it

From the dashboard, press `n` and pick `skills`. Or:

```bash
cs skills viewer home ~
```

## Install libraries from the viewer (`i`)

Press `i` inside the skills viewer to open the **install picker**. It
lists Claude-Code-installable libraries (`gstack` is the headline
entry) and shows what each install does in the detail pane.

Press `enter` to spawn a fresh tmux window in the local `cs` session
running:

```bash
claude '<install prompt for the chosen library>'
```

The viewer immediately switches tmux focus to the new window so the
user lands in the install conversation. Press `Ctrl+q` to go back to
the dashboard, or `prefix+w` to bounce between windows. The install
window keeps running in the background until the user closes it.

Press `esc` to return from the install picker to the skills list.

To add a new installable, append to `All()` in
`internal/apps/skillsviewer/installables.go` — each entry is just a
name, tagline, description, prompt, and tmux window name.

## Verify with `cs doctor`

The `skills` check reports:

- `PASS` — `~/.claude/skills` exists and lists N skills
- `WARN` — `~/.claude/skills not found`. The viewer will be empty.
  Harmless if the user doesn't use skills.

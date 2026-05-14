# Installing Codex (`cs codex`)

> **You are Claude, installing the `codex` binary for pixel-fleet. Do
> this on every machine — local laptop and each SSH remote — where the
> user wants to run Codex sessions.**

## What it is

[OpenAI Codex CLI](https://github.com/openai/codex) — OpenAI's coding
agent. Runs inside a pixel-fleet tmux session like any other app.

## Install (local)

Via npm:

```bash
npm install -g @openai/codex
```

Or on macOS via Homebrew:

```bash
brew install codex
```

**Verify:**

```bash
codex --version
```

## Install (remote)

```bash
ssh <machine> 'npm install -g @openai/codex'
ssh <machine> 'codex --version'
```

If the remote has no global npm prefix set up, install Node.js first
(ask the user — don't pick a Node version manager on their behalf).
Common options the user might already have: `nvm`, `fnm`, distro
package, asdf.

## Authenticate

After install, `codex` requires an OpenAI API key. The user sets this
themselves — pixel-fleet doesn't manage it. Point them at the codex
README if they haven't done it yet.

## Verify with `cs doctor`

```bash
cs doctor
```

The relevant check is:

- `codex installed` — `WARN` if missing (codex sessions will fail to
  launch on that machine but other apps still work)

## Docs

- Repo: <https://github.com/openai/codex>

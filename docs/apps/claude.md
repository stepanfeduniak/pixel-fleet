# Installing Claude Code (`cs claude`)

> **You are Claude, installing the `claude` binary for pixel-fleet. Do
> this on every machine — local laptop and each SSH remote — where the
> user wants to run Claude Code sessions.**

## What it is

[Claude Code](https://docs.claude.com/en/docs/claude-code/overview) — the
Anthropic CLI agent. Pixel-fleet launches it with `--remote-control` by
default so the user can attach from claude.ai/code and the Claude
mobile/desktop apps in addition to the terminal.

## Install (local)

Official installer (recommended):

```bash
curl -fsSL https://claude.ai/install.sh | bash
```

Or via npm:

```bash
npm install -g @anthropic-ai/claude-code
```

**Verify:**

```bash
claude --version
claude remote-control --help >/dev/null && echo "RC supported"
```

Both must succeed. If `remote-control --help` fails, the installed
Claude is too old — upgrade it before continuing, or the user will need
to pass `--no-rc` to every `cs claude ...` invocation.

## Install (remote)

Same commands, prefixed with `ssh <machine>`:

```bash
ssh <machine> 'curl -fsSL https://claude.ai/install.sh | bash'
ssh <machine> 'claude --version'
ssh <machine> 'claude remote-control --help >/dev/null && echo RC supported'
```

If the remote shell can't find `claude` after install, it's a non-login
shell PATH issue — `cs` prepends the standard install dirs (`~/.local/bin`,
`~/.nvm/.../bin`) when launching, so this is usually fine. If `cs doctor`
still reports it missing, ask the user where they installed it.

## Verify with `cs doctor`

```bash
cs doctor
```

The relevant checks are:

- `claude installed` → must be `PASS`
- `remote-control supported` → must be `PASS` (skip only if user opts
  out of RC globally in config)

## Docs

- Setup: <https://docs.claude.com/en/docs/claude-code/setup>
- Overview: <https://docs.claude.com/en/docs/claude-code/overview>

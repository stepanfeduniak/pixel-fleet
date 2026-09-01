# Installation Guide

> **You are Claude, installing pixel-fleet for the user. Run the steps
> in order. After each step, verify before moving on. Stop and ask the
> user if a verification fails.**

---

## 1. Check prerequisites on the local machine

```bash
go version    # need 1.24+
tmux -V       # any recent version
ssh -V
```

If `go` is missing, **ask the user before installing it**. Don't install
Go silently — it touches their toolchain.

---

## 2. Clone and build

```bash
git clone https://github.com/stepanfeduniak/pixel-fleet.git ~/pixel-fleet
cd ~/pixel-fleet
./install.sh
```

`install.sh` builds `cs` and installs it to `~/.local/bin/cs`.

**Verify:**

```bash
command -v cs && cs help | head -1
```

If `cs` isn't on PATH, add `~/.local/bin` to the user's shell rc
(`~/.bashrc` or `~/.zshrc`):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Re-source the rc file and verify again.

---

## 3. The agents

`cs <name> <machine> <path>` opens a **login shell** on the target
machine. There is no agent to choose: you start one inside the session
yourself, and the dashboard recognises it from what it draws.

So the only thing to install is whichever agent binaries the user wants
available on each machine:

| Agent      | What it is                          | Binary   | Install guide                    |
| ---------- | ----------------------------------- | -------- | -------------------------------- |
| Claude Code| Anthropic's CLI agent               | `claude` | [apps/claude.md](apps/claude.md) |
| Codex      | OpenAI Codex CLI                    | `codex`  | [apps/codex.md](apps/codex.md)   |

**Ask the user which agents they want on which machines** before
installing anything. Each guide covers local install, remote install via
ssh, and verification with `cs doctor`.

Two built-in viewers are launched by cs rather than typed, and keep their
own subcommands: `cs apps` browses the registry
([apps/apps.md](apps/apps.md)) and `cs skills` browses `~/.claude/skills`
([apps/skills.md](apps/skills.md)).

---

## 4. Set up each remote machine

For every SSH host alias in `~/.ssh/config` the user plans to run
sessions on, do this **once per machine**:

```bash
# 4a. Enable systemd user-linger so tmux survives SSH drops
ssh <machine> 'sudo loginctl enable-linger $USER'

# 4b. Verify tmux is installed
ssh <machine> 'command -v tmux || echo MISSING'

# 4c. Verify the agents the user wants on this machine
ssh <machine> 'command -v claude || echo MISSING'
ssh <machine> 'command -v codex  || echo MISSING'
```

For each `MISSING`, install that tool on the remote using the same
commands from section 3 (run them via `ssh <machine> '...'`).

---

## 5. Verify the whole fleet

```bash
cs doctor
```

Every check should be `✓`. Common failures and fixes:

| Failure   | Fix                                                       |
| --------- | --------------------------------------------------------- |
| linger    | `ssh <machine> 'sudo loginctl enable-linger $USER'`       |
| tmux      | install tmux on the remote (`sudo apt install tmux`, etc.)|
| claude    | re-run section 3a on the failing machine                  |
| codex     | re-run section 3b on the failing machine                  |

Apply the fix, then re-run `cs doctor` until everything passes.

---

## 6. Hand off to the user

Once `cs doctor` is clean, tell the user:

- Run `cs` to open the dashboard.
- A session is a shell: `cs work gpu-01 ~/proj`, then type `claude` in it.
- Run `cs help` to see all commands and keybindings.
- Optional config: `~/.config/cs/config.yaml`
- Logs: `~/.config/cs/cs.log`

---

## Optional: configuration

Defaults are fine for most users. To override anything, create
`~/.config/cs/config.yaml`:

```yaml
session_name: cs                 # local tmux session name
remote_tmux_session: cs-remote   # tmux session name on remote machines
claude_bin: claude               # local agent binaries
codex_bin: codex
remote_claude_bin: claude        # remote agent binaries (PATH-resolved)
remote_codex_bin: codex
clipboard: true                  # copy/paste + URL bindings (docs/clipboard.md)
refresh_interval: 2s
discovery_interval: 60s          # set to -1s to disable orphan auto-scan
```

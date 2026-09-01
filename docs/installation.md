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

## 3. The apps

`cs` is a launcher. The actual work happens inside one of these apps,
which you pick per session (`cs <app> <name> <machine> <path>`).

| App        | What it is                          | Needs binary?  | Install guide                |
| ---------- | ----------------------------------- | -------------- | ---------------------------- |
| `claude`   | Claude Code CLI                     | yes — `claude` | [apps/claude.md](apps/claude.md)     |
| `codex`    | OpenAI Codex CLI                    | yes — `codex`  | [apps/codex.md](apps/codex.md)       |
| `terminal` | Plain login shell, no agent         | no             | [apps/terminal.md](apps/terminal.md) |
| `apps`     | Built-in: browse registered apps    | no             | [apps/apps.md](apps/apps.md)         |
| `skills`   | Built-in: browse `~/.claude/skills` | no             | [apps/skills.md](apps/skills.md)     |

**Ask the user which agents they want on which machines** before
installing anything. Then open the matching per-app guide above and
follow it — each one covers local install, remote install via ssh, and
verification with `cs doctor`.

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
remote_control: true             # default --remote-control on Claude sessions
clipboard: true                  # copy/paste + URL bindings (docs/clipboard.md)
refresh_interval: 2s
discovery_interval: 60s          # set to -1s to disable orphan auto-scan
```

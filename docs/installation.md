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

## 3. Set up each remote machine

For every SSH host alias in `~/.ssh/config` the user plans to run
sessions on, do this **once per machine**:

```bash
# 3a. Enable systemd user-linger so tmux survives SSH drops
ssh <machine> 'sudo loginctl enable-linger $USER'

# 3b. Make sure tmux is installed
ssh <machine> 'command -v tmux || echo MISSING'

# 3c. Make sure the agents they want are installed
ssh <machine> 'command -v claude || echo MISSING'
ssh <machine> 'command -v codex  || echo MISSING'
```

**Ask the user which agents they want on which machines** before
installing anything remotely. Don't push `claude` or `codex` onto a
machine the user didn't ask for.

---

## 4. Verify the whole fleet

```bash
cs doctor
```

Every check should be `✓`. Common failures and fixes:

| Failure   | Fix                                                       |
| --------- | --------------------------------------------------------- |
| linger    | `ssh <machine> 'sudo loginctl enable-linger $USER'`       |
| tmux      | install tmux on the remote (`sudo apt install tmux`, etc.)|
| claude    | install/upgrade `claude-code` on the remote               |
| codex     | install `codex` on the remote                             |

Apply the fix, then re-run `cs doctor` until everything passes.

---

## 5. Hand off to the user

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
refresh_interval: 2s
discovery_interval: 60s          # set to -1s to disable orphan auto-scan
```

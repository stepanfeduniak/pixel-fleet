# pixel-fleet

A multi-machine TUI for managing persistent agent sessions across your
laptop and any SSH-accessible host. Run **Claude Code**, **Codex**, or a
plain terminal on the right machine for the job — and never lose a
session to an SSH drop, laptop sleep, or a closed terminal.

```
  cs                                     ┌─ pixel-fleet ─ 4 sessions ─┐
  ───────────────────────────────────────│                            │
  ▸ training     gpu-01    ~/ml-project  │  ● training    CLAUDE  RC  │
    frontend     home      ~/webapp      │  ● frontend    CLAUDE      │
    review       gpu-01    ~/proj        │  ● review      CODEX       │
    shell        h100      ~/data        │  ● shell       TERM        │
                                         └────────────────────────────┘
```

## Why

Working across multiple machines (a laptop, a GPU box, a couple of remote
servers) means juggling SSH tabs, losing context every time the network
hiccups, and re-opening Claude/Codex sessions over and over.

`pixel-fleet` puts every agent session inside a tmux session **on the
target machine**, so:

- The agent keeps running when your SSH connection dies, when your laptop
  sleeps, when you close the terminal — anything short of the remote
  machine itself going down.
- You see all sessions across all machines in one grid view.
- Claude sessions launch with `--remote-control`, so you can attach from
  [claude.ai/code](https://claude.ai/code) and the Claude mobile/desktop
  apps in addition to the terminal.

## Features

- **Persistent remote sessions** — each session lives in a long-running
  tmux session on the target host, surviving disconnects.
- **Single-pane dashboard** — grid view of every session across every
  machine, with live status.
- **Three agents**: Claude Code, Codex, and plain terminal — pick per
  session.
- **Orphan discovery** — finds agent sessions running on remote machines
  that aren't tracked locally, lets you adopt them.
- **Archive** — hide finished sessions from the grid without killing them.
- **SSH config aware** — every host in your `~/.ssh/config` becomes
  available automatically.
- **Preflight diagnostics** — `cs doctor` checks every known machine for
  required tools and configuration.

## Requirements

- **Local**: Go 1.24+ (to build), `tmux`, `ssh`
- **Each remote machine**: `tmux`, the agent binary you want to launch
  there (`claude`, `codex`), and **systemd user-linger enabled** so the
  remote tmux session can outlive your SSH connection:
  ```bash
  ssh <machine> 'sudo loginctl enable-linger $USER'
  ```
- Claude Code or Codex installed and on `PATH` wherever you want to use
  them.

Run `cs doctor` to verify all known machines.

## Install

```bash
git clone https://github.com/stepanfeduniak/pixel-fleet.git
cd pixel-fleet
./install.sh
```

This builds the `cs` binary and installs it to `~/.local/bin/cs`. Make
sure `~/.local/bin` is on your `PATH`.

## Usage

```bash
cs                                          # open the dashboard
cs claude training gpu-01 ~/ml-project      # new Claude session on gpu-01
cs codex review gpu-01 ~/ml-project         # new Codex session
cs term shell home ~/webapp                 # plain shell, no agent
cs ls                                       # list sessions across all machines
cs scan                                     # find orphaned remote sessions
cs adopt 3 my-session                       # adopt orphan #3 with a name
cs kill training                            # kill one session
cs doctor                                   # preflight all known machines
```

The first positional after `claude`/`codex`/`term` is the **session name**
(your label), then the **machine** (an SSH host alias or `home` for
local), then the **path** to start in.

Pass `--no-rc` to a Claude session to skip `--remote-control`.

### Dashboard keys

| Key | Action |
|---|---|
| arrows / `hjkl` | navigate the grid |
| `enter` | focus into a session |
| `n` | new session |
| `x` | kill selected session |
| `a` | archive selected session |
| `A` | open archive view |
| `s` | scan machines for orphans |
| `r` | refresh |
| `q` | detach (everything keeps running) |
| `?` | help |

To return to the dashboard from inside a session: `F1`, `Ctrl+q`, or
`Ctrl+b q`.

## Configuration

Optional config at `~/.config/cs/config.yaml`. Defaults are usually fine:

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

Logs: `~/.config/cs/cs.log`

## License

MIT — see [LICENSE](LICENSE).

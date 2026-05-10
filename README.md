# pixel-fleet

A multi-machine TUI (`cs`) for persistent Claude Code / Codex / shell
sessions across your laptop and any SSH host. Sessions live in tmux on
the target machine and survive disconnects.

## Install

Point Claude (or yourself) at the installation guide:

**[docs/installation.md](docs/installation.md)**

It's written so another Claude can execute it end-to-end — clone, build,
set up each remote machine, verify with `cs doctor`.

## Usage

```bash
cs            # open the dashboard
cs help       # all commands and keybindings
cs doctor     # preflight all known machines
```

## License

MIT — see [LICENSE](LICENSE).

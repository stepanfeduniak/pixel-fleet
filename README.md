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

## Copy, paste, and links

Dragging with the mouse in any session — local or remote — puts the
selection on your machine's clipboard. `prefix u` lists the URLs on
screen and opens the one you pick in your browser.

Remote sessions are a nested tmux, so the selection is made on the far
host; cs bridges it back over OSC 52. On by default, `clipboard: false`
to turn it off.

**[docs/clipboard.md](docs/clipboard.md)**

## Coding blocker

Watching agents work is not working. Press `b` in the dashboard, pick a
duration, and for that long you can see the fleet but can't go into a
session. Sessions keep running; only you are locked out. It survives a
restart, and takes a typed `break` to end early.

**[docs/blocker.md](docs/blocker.md)**

## License

MIT — see [LICENSE](LICENSE).

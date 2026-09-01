# Copy, paste, and links

Selecting text in a cs session puts it on your machine's clipboard, and
links on screen can be opened in your browser — in local sessions and in
remote ones. This is on by default; nothing to install.

## Keys

| Action | What happens |
| --- | --- |
| Drag with the mouse | Selection goes to the system clipboard |
| Double-click | Copies the word |
| Triple-click | Copies the line |
| `prefix` `u` | Menu of the URLs on screen — press its number to open it |
| `prefix` `U` | Copies the newest URL on screen |
| `prefix` `C-v` | Pastes the system clipboard into the pane |
| `prefix` `[` | Scrollback / copy mode; `Enter` copies the selection |

`prefix` is tmux's, `Ctrl-b` unless you changed it. `cs urls` and
`cs urls --copy` do the same as `prefix u` / `prefix U` from a shell.

## Why this needs any code at all

cs turns tmux mouse mode on, so that scrolling and clicking between panes
work. The cost is that **tmux**, not your terminal emulator, receives every
mouse event: the terminal's own drag-to-select never happens, and a
selection lands in a tmux paste buffer that the OS clipboard knows nothing
about. So cs binds the copy commands to pipe through `pbcopy` (macOS) or
`wl-copy` / `xclip` / `xsel` (Linux).

Remote sessions need more than that, because a remote session is a nested
tmux:

```
Terminal.app
└── local tmux            <- cs dashboard and session windows
    └── ssh
        └── remote tmux   <- the agent actually runs here
            └── claude
```

The inner tmux enables mouse reporting, which sets `#{mouse_any_flag}` on
the outer pane, so the outer tmux forwards mouse events straight through
instead of handling them. The selection is therefore made by the **remote**
tmux — on a machine with no access to your laptop's clipboard.

What the remote tmux can do is emit the selection as an
[OSC 52](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html) clipboard
escape sequence. cs sets that up on both ends:

1. **Remote** (`clipboardBridgeSnippet` in `internal/session/session.go`):
   `set-clipboard on` plus `terminal-features ,*:clipboard`, so every copy
   emits OSC 52 down the ssh TTY. `terminal-features` is forced because the
   ssh TTY inherits `TERM` from the outer tmux pane, and not every terminfo
   entry advertises the capability that gates the sequence.
2. **Local** (`ConfigureClipboard` in `internal/tmux/clipboard.go`):
   `set-clipboard on` makes the local tmux *accept* an incoming OSC 52 into
   a buffer, and the `pane-set-clipboard` hook copies that buffer to the OS
   clipboard.

The same hook means anything else that emits OSC 52 — including agents
running in a local pane — reaches your clipboard too.

## How it stays applied

The settings live on a tmux server, not in a file it reads, so cs reapplies
them rather than assuming they stuck:

- **Locally**, on every `cs` launch and whenever a session is created or
  adopted. The tmux server outlives cs, so a server started by an older
  version still gets them.
- **On remotes**, three ways: the launch bootstrap when a session starts,
  the reattach command when one is adopted, and the background discovery
  scan, which already reaches every known machine on an interval and so
  carries the settings along for free. That last one is what covers
  sessions cs did not start — launched by an older cs, or adopted from
  outside it.

Because the remote settings are server-global, configuring one host covers
every tmux session on it, not only the cs-managed ones. The scan never
applies the *remote* configuration to `home`: locally, copies go through
`pbcopy` directly, and the remote bindings would replace that with an
OSC 52 emission the terminal discards.

## Opening links

`prefix u` runs `cs urls` against the pane, which reads its content, pulls
out the URLs and offers the nine most recent in a `display-menu`. Picking
one hands it to `open` (macOS) or `xdg-open` (Linux) as an argument vector,
never through a shell, so query strings and other punctuation survive
intact.

This reads the *local* pane, which is why it works for remote sessions too:
the outer pane holds the rendered remote screen. Two consequences worth
knowing:

- A URL wrapped across rows is rejoined by hand (a row that exactly fills
  the pane is glued to the next), because `capture-pane -J` cannot rejoin
  wrapping that a *remote* tmux performed.
- In a remote session only what is currently on screen is searchable. The
  outer pane has no meaningful scrollback of its own — the remote tmux is a
  full-screen application in it. Scroll the remote session up first if the
  link has already gone past.

## Turning it off

The bindings and hooks are server-global; tmux has no per-session key
tables. If you keep your own copy bindings in `~/.tmux.conf` and would
rather cs left them alone, put this in `~/.config/cs/config.yaml`:

```yaml
clipboard: false
```

That drops the local bindings *and* the remote bridge. It takes effect for
sessions started afterwards; bindings already installed on a running tmux
server stay until that server exits.

## If a copy does not arrive

- `tmux show -sv set-clipboard` should print `on`, locally and on the
  remote. cs sets both; a `clipboard: false` config or an ancient tmux is
  the usual reason it is not.
- `tmux show-hooks -g pane-set-clipboard` should show the `run-shell` that
  pipes to your clipboard tool. Without it, remote copies reach a local
  tmux buffer (`prefix ]` pastes them) but not the OS clipboard.
- On Linux, cs needs `wl-copy`, `xclip` or `xsel` on the **local** machine.
  With none of them installed it leaves tmux's defaults alone rather than
  binding copies to a tool that is not there.
- macOS Terminal.app cannot render clickable links at all — that is a
  Terminal.app limitation, not a tmux one, and is what `prefix u` is for.

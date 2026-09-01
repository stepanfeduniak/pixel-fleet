package appviewer

import "github.com/stepanfeduniak/pixel-fleet/internal/apps/installables"

// Installables returns the entries shown when the user presses `i`
// inside the app viewer. These are the underlying CLIs that the
// registered apps wrap — installing them is what makes the
// corresponding cs app actually usable on this machine.
//
// Adding an entry is the whole extension surface: append a record and
// the picker lists it.
func Installables() []installables.Installable {
	return []installables.Installable{
		{
			Name:    "codex",
			Tagline: "OpenAI Codex CLI (the binary the `codex` app wraps)",
			Body: `Codex is OpenAI's agentic CLI. cs registers a ` + "`codex`" + ` app
that launches it inside a tmux window, but the binary needs to be
installed and authed first.

This install spawns a new Claude session that will:
  1. Install the Codex CLI (typically ` + "`npm install -g @openai/codex`" + `
     or the brew formula, depending on platform).
  2. Run ` + "`codex login`" + ` so the binary has credentials.
  3. Verify with ` + "`codex --version`" + `.`,
			Prompt: "Please install OpenAI's Codex CLI on this machine. " +
				"Install it the recommended way for my platform (npm install -g @openai/codex, brew, or whatever OpenAI's docs currently say), " +
				"then run `codex login` so it has credentials, and verify with `codex --version`. " +
				"After it's working, briefly explain how to launch a Codex session from pixel-fleet (cs codex <name> <machine> <path>).",
			WindowName: "install-codex",
		},
		{
			Name:    "claude-code",
			Tagline: "Anthropic's Claude Code CLI (the binary the `claude` app wraps)",
			Body: `Claude Code is the CLI that backs cs's headline ` + "`claude`" + ` app.
If you can read this, it's probably already installed — but on a
fresh machine, this is the install that unlocks every other claude
session.

This install spawns a new Claude session that will:
  1. Install or upgrade ` + "`claude`" + ` via the official installer
     (` + "`npm install -g @anthropic-ai/claude-code`" + ` or curl-script).
  2. Run ` + "`claude /login`" + ` so the binary has credentials.
  3. Verify with ` + "`claude --version`" + ` and confirm
     the binary is on PATH.`,
			Prompt: "Please install (or upgrade) Anthropic's Claude Code CLI on this machine. " +
				"Use the official installer — `npm install -g @anthropic-ai/claude-code` or the recommended curl-script, whichever Anthropic's docs currently say. " +
				"Run `claude /login` if it's not already authed. " +
				"Verify with `claude --version`.",
			WindowName: "install-claude-code",
		},
	}
}

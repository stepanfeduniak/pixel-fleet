package skillsviewer

import "github.com/stepanfeduniak/pixel-fleet/internal/apps/installables"

// Installables returns the entries shown when the user presses `i`
// inside the skills viewer. The picker mechanism itself lives in the
// installables package — this function is just the per-viewer list.
//
// Adding an entry is the whole extension surface: append a record and
// the picker lists it.
func Installables() []installables.Installable {
	return []installables.Installable{
		{
			Name:    "gstack",
			Tagline: "Stepan's Claude Code skill pack (ship, qa, codex, browse, …)",
			Body: `gstack bundles ~30 Claude Code skills for shipping code,
QA testing, design review, code review, security audits, performance
benchmarking, and more. It's distributed as a Claude Code plugin
marketplace.

The install spawns a new Claude session that will:
  1. Add the marketplace (` + "`/plugin marketplace add stepanfeduniak/gstack`" + `).
  2. Install the gstack plugin (` + "`/plugin install gstack@gstack`" + `).

Skills land in ~/.claude/plugins/ and show up in this viewer on
the next refresh. After install, try /ship --help or /qa --help.`,
			Prompt: "Please install gstack for me — it's Stepan Feduniak's Claude Code skill pack, " +
				"distributed as a plugin marketplace at github.com/stepanfeduniak/gstack. " +
				"Walk me through the install: add the marketplace with `/plugin marketplace add stepanfeduniak/gstack`, " +
				"then install the plugin with `/plugin install gstack@gstack`. Confirm any prompts. " +
				"When done, run `/ship --help` to verify the install worked.",
			WindowName: "install-gstack",
		},
	}
}

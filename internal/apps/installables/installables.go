// Package installables provides a shared "install a library / CLI"
// picker that any in-process viewer (skills viewer, app viewer, …) can
// overlay on top of its viewerui.Source.
//
// Pressing `i` inside the wrapped viewer switches from the regular
// browse pane to an install picker; pressing `enter` on an item spawns
// a new tmux window in the local cs session running Claude with the
// item's Prompt as the first user message. Claude then walks the user
// through whatever shell / slash commands the install needs.
//
// Each viewer keeps its own list of installables next to its data
// (e.g. skillsviewer.Installables, appviewer.Installables) and passes
// it into Run().
package installables

// Installable is one offer the picker shows. The install always runs
// as `claude '<Prompt>'` inside a fresh tmux window — there's no
// special-casing per item, which is what keeps the picker generic.
type Installable struct {
	// Name is the human label shown in the picker list. Lowercase, short.
	Name string
	// Tagline is one line shown next to the name. Keep it scannable.
	Tagline string
	// Body is the detail-pane text. Multi-line. Explain what gets
	// installed, where it lands, and what the user can do after.
	Body string
	// Prompt is the first user message sent to Claude in the spawned
	// install window. Claude treats it as the conversation opener.
	Prompt string
	// WindowName is the tmux window name for the spawned install
	// session. Stays visible in the dashboard until the user closes it.
	WindowName string
}

package apps

import "strings"

// TerminalName is the canonical name of the fallback app — a plain login
// shell. A session is a terminal until something recognisable is detected
// running inside it.
const TerminalName = "terminal"

// ForSession returns the app a session's agent name refers to, falling back
// to the terminal.
//
// Use this rather than Resolve wherever a session's name is being turned into
// an app. Resolve falls back to the registry's *first registered* app, which
// is an artefact of package import order — today that is claude, so an
// unrecognised or not-yet-detected name would silently mean "claude", and in
// the launch path would actually exec it. A session is a shell until proven
// otherwise.
func ForSession(name string) App {
	if a, ok := Lookup(name); ok {
		return a
	}
	if a, ok := Lookup(TerminalName); ok {
		return a
	}
	return Default()
}

// Marker is a substring whose presence in a captured pane is evidence that a
// particular app is running. Weight is how much that evidence is worth: a
// marker unique to one agent's chrome scores high, a suggestive-but-shared one
// scores low.
//
// Weights exist because the agents share vocabulary — "esc to interrupt"
// appears in both Claude's and Codex's footers — so detection cannot be a
// first-substring-wins scan. Each app scores only what is distinctive to it,
// and the highest total wins.
type Marker struct {
	Text string
	// Weight contributed once if the marker is present, however many times
	// it occurs. Repetition is not extra evidence.
	Weight int
	// LineStart restricts the match to the beginning of a line, ignoring
	// leading whitespace. Use it for prompt and bullet glyphs, which mean
	// nothing in the middle of a sentence the agent happens to have printed.
	LineStart bool
}

// Score totals the weights of the markers present in content.
func Score(content string, markers []Marker) int {
	var lines []string
	needLines := false
	for _, m := range markers {
		if m.LineStart {
			needLines = true
			break
		}
	}
	if needLines {
		for _, line := range strings.Split(content, "\n") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}

	total := 0
	for _, m := range markers {
		if m.Text == "" {
			continue
		}
		if !m.LineStart {
			if strings.Contains(content, m.Text) {
				total += m.Weight
			}
			continue
		}
		for _, line := range lines {
			if strings.HasPrefix(line, m.Text) {
				total += m.Weight
				break
			}
		}
	}
	return total
}

// DetectFromPane returns the app whose markers best explain a captured pane,
// or nil when nothing scores. A tie is treated as no answer: two apps with
// equal evidence means the pane is ambiguous, and guessing would flip the
// dashboard's badge back and forth between refreshes.
func DetectFromPane(content string) App {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	mu.RLock()
	defer mu.RUnlock()

	var best App
	bestScore, tied := 0, false
	for _, a := range registered {
		score := a.MatchesPane(content)
		switch {
		case score > bestScore:
			best, bestScore, tied = a, score, false
		case score == bestScore && score > 0:
			tied = true
		}
	}
	if best == nil || tied {
		return nil
	}
	return best
}

// DetectFromProcess returns the app that claims the given foreground process
// name (tmux's pane_current_command), or nil.
//
// This is the strongest signal available, but only for sessions on "home":
// a remote session's local pane is running ssh, so the process there says
// nothing about what is running on the far side. Those fall back to
// DetectFromPane, which reads the rendered remote screen.
func DetectFromProcess(processName string) App {
	if strings.TrimSpace(processName) == "" {
		return nil
	}
	mu.RLock()
	defer mu.RUnlock()
	for _, a := range registered {
		if a.ProcessMatches(processName) {
			return a
		}
	}
	return nil
}

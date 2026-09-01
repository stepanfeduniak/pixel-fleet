package tmux

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// urlPattern matches http(s) and file URLs. The excluded set is deliberately
// wide: terminal output surrounds URLs with quotes, brackets and box-drawing
// punctuation, none of which belong to the link itself.
var urlPattern = regexp.MustCompile(`(?i)\b(?:https?|file)://[^\s"'` + "`" + `<>()\[\]{}|\\^]+`)

// maxURLMenuItems is how many URLs the prefix+u menu offers. The menu is keyed
// 1-9, so nine is the natural ceiling.
const maxURLMenuItems = 9

// urlScrollback is how many lines of pane history to search. Remote panes
// render a full-screen nested tmux and so carry almost no outer scrollback,
// but a generous window costs nothing for local panes.
const urlScrollback = 3000

// ExtractURLs returns the unique URLs found in raw pane content, in the order
// they appear.
//
// width is the pane width in columns. Terminal output wraps a long URL across
// several rows, and tmux's own -J flag cannot rejoin them for a remote session
// (the wrapping was done by the *remote* tmux, so the outer pane sees ordinary
// separate rows). Instead any row that exactly fills the pane is joined to the
// next one, which is what wrapping means in both the local and nested case.
// Pass width <= 0 to disable joining.
func ExtractURLs(content string, width int) []string {
	text := content
	if width > 0 {
		var b strings.Builder
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			b.WriteString(line)
			// A row of exactly `width` runes was wrapped, not ended.
			if i < len(lines)-1 && utf8.RuneCountInString(line) != width {
				b.WriteString("\n")
			}
		}
		text = b.String()
	}

	var urls []string
	seen := map[string]bool{}
	for _, m := range urlPattern.FindAllString(text, -1) {
		// Sentence punctuation that trails a URL in prose is not part of it.
		m = strings.TrimRight(m, ".,;:!?")
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		urls = append(urls, m)
	}
	return urls
}

// PaneURLs captures a pane and returns the URLs on it, most recent first.
func PaneURLs(paneID string) ([]string, error) {
	width := 0
	if out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_width}").Output(); err == nil {
		width, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}

	out, err := exec.Command("tmux", "capture-pane", "-p",
		"-S", fmt.Sprintf("-%d", urlScrollback), "-t", paneID).Output()
	if err != nil {
		return nil, fmt.Errorf("capture-pane: %w", err)
	}

	urls := ExtractURLs(string(out), width)
	// Newest first: the link a user just saw scroll past is the one they want.
	for i, j := 0, len(urls)-1; i < j; i, j = i+1, j-1 {
		urls[i], urls[j] = urls[j], urls[i]
	}
	return urls, nil
}

// urlListPath is the scratch file a URL menu writes its choices to. Menu items
// reference a line number in this file rather than embedding the URL itself,
// which keeps arbitrary URL text out of the nested tmux/shell quoting layers.
func urlListPath(paneID string) string {
	safe := strings.NewReplacer("%", "", "/", "", ".", "", " ", "").Replace(paneID)
	return filepath.Join(os.TempDir(), "cs-urls-"+safe+".txt")
}

// ShowURLMenu displays a tmux menu of the URLs on a pane; choosing one opens it
// in the local browser. Bound to prefix+u.
func ShowURLMenu(paneID string) error {
	urls, err := PaneURLs(paneID)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return exec.Command("tmux", "display-message", "no URLs on screen").Run()
	}
	if len(urls) > maxURLMenuItems {
		urls = urls[:maxURLMenuItems]
	}

	listPath := urlListPath(paneID)
	if err := os.WriteFile(listPath, []byte(strings.Join(urls, "\n")+"\n"), 0600); err != nil {
		return fmt.Errorf("write URL list: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cs binary: %w", err)
	}

	args := []string{"display-menu", "-T", " open URL ", "-t", paneID}
	// A menu is drawn on a client, and tmux infers the current one from the
	// caller's environment. That is right when this runs from a key binding,
	// but wrong when `cs urls` is typed in one pane about another, so name
	// the client attached to the target's session explicitly.
	if c := clientForPane(paneID); c != "" {
		args = append(args, "-c", c)
	}
	for i, u := range urls {
		// The menu entry names a line number, never the URL text, so nothing
		// user-controlled has to survive tmux's parser and then a shell.
		shellCmd := fmt.Sprintf("%s --open-url %s %d", shQuote(self), shQuote(listPath), i+1)
		args = append(args, menuLabel(u), strconv.Itoa(i+1), "run-shell "+tmuxQuote(shellCmd))
	}
	return exec.Command("tmux", args...).Run()
}

// clientForPane returns a client attached to the session owning paneID, or ""
// if the session has none attached.
func clientForPane(paneID string) string {
	out, err := exec.Command("tmux", "list-clients", "-t", paneID, "-F", "#{client_name}").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			return line
		}
	}
	return ""
}

// menuLabel shortens a URL for display without changing what gets opened.
func menuLabel(u string) string {
	const max = 70
	if utf8.RuneCountInString(u) <= max {
		return tmuxEscape(u)
	}
	r := []rune(u)
	return tmuxEscape(string(r[:max-3]) + "...")
}

// OpenURLFromList opens the nth (1-based) URL recorded by ShowURLMenu. The URL
// is passed to the opener as an argument vector, never through a shell, so no
// quoting of user-visible text is involved.
func OpenURLFromList(listPath string, n int) error {
	f, err := os.Open(listPath)
	if err != nil {
		return err
	}
	defer f.Close()

	i := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		i++
		if i == n {
			return exec.Command(openerBin(), scanner.Text()).Run()
		}
	}
	return fmt.Errorf("no URL %d in %s", n, listPath)
}

// CopyNewestURL puts the most recent URL on a pane onto the OS clipboard.
// Bound to prefix+U.
func CopyNewestURL(paneID string) error {
	urls, err := PaneURLs(paneID)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return exec.Command("tmux", "display-message", "no URLs on screen").Run()
	}
	copyCmd := clipboardCopyCmd()
	if copyCmd == "" {
		return fmt.Errorf("no clipboard tool available")
	}
	cmd := exec.Command("sh", "-c", copyCmd)
	cmd.Stdin = strings.NewReader(urls[0])
	if err := cmd.Run(); err != nil {
		return err
	}
	return exec.Command("tmux", "display-message", "copied "+menuLabel(urls[0])).Run()
}

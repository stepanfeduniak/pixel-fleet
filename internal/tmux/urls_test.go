package tmux

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
		want    []string
	}{
		{
			name:    "plain",
			content: "open https://example.com/a and http://example.org/b",
			want:    []string{"https://example.com/a", "http://example.org/b"},
		},
		{
			name:    "trailing sentence punctuation is not part of the URL",
			content: "see https://example.com/docs.\nor https://example.com/other,",
			want:    []string{"https://example.com/docs", "https://example.com/other"},
		},
		{
			name:    "surrounding quotes and brackets are stripped",
			content: `a "https://example.com/q" b (https://example.com/p) c [https://example.com/s]`,
			want:    []string{"https://example.com/q", "https://example.com/p", "https://example.com/s"},
		},
		{
			name:    "duplicates collapse, first position wins",
			content: "https://example.com/x\nhttps://example.com/y\nhttps://example.com/x",
			want:    []string{"https://example.com/x", "https://example.com/y"},
		},
		{
			name:    "query strings survive intact",
			content: "https://claude.ai/x?product_surface=cli&mode=auth",
			want:    []string{"https://claude.ai/x?product_surface=cli&mode=auth"},
		},
		{
			name:    "file URLs",
			content: "file:///Users/me/report.html",
			want:    []string{"file:///Users/me/report.html"},
		},
		{
			name:    "no URLs",
			content: "nothing to see here",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractURLs(tt.content, tt.width)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractURLs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// A URL long enough to wrap arrives as separate rows when the wrapping was
// done by a *remote* tmux, so capture-pane's own -J cannot rejoin it. Rows
// that exactly fill the pane are joined to the next one instead.
func TestExtractURLsRejoinsWrappedRows(t *testing.T) {
	const width = 40
	full := "https://example.com/very/long/path/that/wraps/across/rows?x=1"
	row1, row2 := full[:width], full[width:]
	if len(row1) != width {
		t.Fatalf("test setup: row1 is %d cols, want %d", len(row1), width)
	}

	content := "prefix line\n" + row1 + "\n" + row2 + "\ntrailing line"
	got := ExtractURLs(content, width)
	if len(got) != 1 || got[0] != full {
		t.Errorf("ExtractURLs() = %#v, want [%s]", got, full)
	}
}

func TestExtractURLsDoesNotJoinShortRows(t *testing.T) {
	// Neither row fills the pane, so they are separate lines and the second
	// must not be glued onto the first.
	content := "https://example.com/a\nnot-part-of-it"
	got := ExtractURLs(content, 40)
	if len(got) != 1 || got[0] != "https://example.com/a" {
		t.Errorf("ExtractURLs() = %#v, want [https://example.com/a]", got)
	}
}

func TestMenuLabelTruncatesButEscapes(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("x", 200)
	label := menuLabel(long)
	if len([]rune(label)) > 70 {
		t.Errorf("menuLabel() is %d runes, want <= 70", len([]rune(label)))
	}
	// '#' would otherwise start a tmux format inside the menu entry.
	if got := menuLabel("https://example.com/p#frag"); !strings.Contains(got, "##frag") {
		t.Errorf("menuLabel() = %q, want '#' escaped", got)
	}
}

func TestTmuxQuote(t *testing.T) {
	if got, want := tmuxQuote(`/usr/bin/cs`), `"/usr/bin/cs"`; got != want {
		t.Errorf("tmuxQuote() = %s, want %s", got, want)
	}
	if got, want := tmuxQuote(`a"b`), `"a\"b"`; got != want {
		t.Errorf("tmuxQuote() = %s, want %s", got, want)
	}
	if got, want := tmuxQuote(`a#{x}b`), `"a##{x}b"`; got != want {
		t.Errorf("tmuxQuote() = %s, want %s", got, want)
	}
}

func TestListsClipboardFeature(t *testing.T) {
	// Real `tmux show -sv terminal-features` output: one entry per line.
	const present = `xterm*:clipboard:ccolour:cstyle:focus:title
screen*:title
rxvt*:ignorefkeys
*:clipboard`
	if !listsClipboardFeature(present) {
		t.Error("did not find *:clipboard; cs would append a duplicate on every run")
	}

	const absent = `xterm*:clipboard:ccolour:cstyle:focus:title
screen*:title
rxvt*:ignorefkeys`
	if listsClipboardFeature(absent) {
		t.Error("matched xterm*:clipboard as the wildcard entry")
	}

	if listsClipboardFeature("") {
		t.Error("empty output must not report the feature as present")
	}
	if !listsClipboardFeature("screen*:title,*:clipboard") {
		t.Error("comma-joined entries on one line must also be matched")
	}
}

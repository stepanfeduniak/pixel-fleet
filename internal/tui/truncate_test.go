package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Cell content used to be cut with a byte slice (clean[:width-1]). Pane
// captures are mostly multi-byte: every box-drawing character an agent draws
// its separators from is three bytes, so the cut landed mid-rune and the
// terminal rendered the remains as U+FFFD. The same byte length also made a
// separator line look three times longer than it is, truncating it far
// earlier than the cell needed.

func TestTruncateContentKeepsRunesIntact(t *testing.T) {
	// The exact shape that showed up in the dashboard: a Claude Code input
	// separator, far wider than the cell.
	line := strings.Repeat("─", 200)

	got := truncateContent(line, 60, 1)

	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("truncation produced a replacement character: %q", got)
	}
	if w := cellWidthOf(got); w > 60 {
		t.Errorf("truncated to %d columns, want <= 60", w)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis on a truncated line, got %q", got)
	}
}

// The old code measured in bytes, so a line of 3-byte runes was cut at
// roughly a third of the columns actually available and cells looked ragged.
func TestTruncateContentMeasuresColumnsNotBytes(t *testing.T) {
	line := strings.Repeat("─", 100) // 100 columns, 300 bytes

	got := truncateContent(line, 80, 1)

	// 80 columns of content: the ellipsis replaces the last one.
	if w := cellWidthOf(got); w < 70 {
		t.Errorf("only %d columns used of the 80 available (byte-based measurement?): %q", w, got)
	}
}

func TestTruncateContentLeavesShortLinesAlone(t *testing.T) {
	for _, line := range []string{"", "plain ascii", "─── short ───"} {
		if got := truncateContent(line, 60, 1); got != line {
			t.Errorf("truncateContent(%q) = %q, want it unchanged", line, got)
		}
	}
}

// Wide runes take two columns each; counting them as one overflows the cell
// and pushes the border out of alignment.
func TestTruncateCellsAccountsForWideRunes(t *testing.T) {
	// 10 CJK runes = 20 columns.
	if got, want := cellWidthOf("日本語日本語日本語日"), 20; got != want {
		t.Errorf("cellWidthOf = %d, want %d", got, want)
	}

	got := truncateCells("日本語日本語日本語日", 10)
	if w := cellWidthOf(got); w > 10 {
		t.Errorf("truncated to %d columns, want <= 10: %q", w, got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8: %q", got)
	}
}

// A cell narrow enough to leave no room must not panic — the old
// clean[:width-1] underflowed to a negative index.
func TestTruncateCellsHandlesZeroAndNegativeWidth(t *testing.T) {
	for _, w := range []int{0, -1, -100} {
		if got := truncateCells("some content", w); got != "" {
			t.Errorf("truncateCells(_, %d) = %q, want empty", w, got)
		}
	}
	if got := truncateContent("some content", 0, 1); got != "" {
		t.Errorf("truncateContent with zero width = %q, want empty", got)
	}
}

// truncateContent keeps the most recent output, so the tail of the capture is
// what survives the height clamp.
func TestTruncateContentKeepsLastLines(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"

	got := truncateContent(content, 80, 2)

	if want := "line4\nline5"; got != want {
		t.Errorf("truncateContent = %q, want %q", got, want)
	}
}

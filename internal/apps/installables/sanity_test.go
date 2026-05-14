package installables_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/installables"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/viewerui"
)

type srcStub struct{ items []installables.Installable }

func (s *srcStub) Title() string            { return "Picker test   1 available   [i] install" }
func (s *srcStub) Count() int               { return len(s.items) }
func (s *srcStub) ListLine(i int) string    { return s.items[i].Name }
func (s *srcStub) Detail(i int) string      { return s.items[i].Body }

// Regression: viewerui's body panels were rendered 2 rows / 2 cols taller
// than the requested size (lipgloss adds border on top of Width/Height),
// causing the header to scroll off the top of the alt-screen.
func TestRenderedSizeFitsTerminal(t *testing.T) {
	cases := []struct{ w, h int }{
		{209, 50},
		{120, 30},
		{80, 24},
	}
	for _, c := range cases {
		m := viewerui.NewModel(&srcStub{items: []installables.Installable{
			{Name: "x", Body: "body"},
		}})
		next, _ := m.Update(tea.WindowSizeMsg{Width: c.w, Height: c.h})
		m = next.(viewerui.Model)
		view := m.View()
		lines := strings.Split(view, "\n")
		var maxW int
		for _, ln := range lines {
			w := len([]rune(stripANSI(ln)))
			if w > maxW {
				maxW = w
			}
		}
		if len(lines) > c.h {
			t.Errorf("%dx%d: rendered %d lines (overflows by %d)", c.w, c.h, len(lines), len(lines)-c.h)
		}
		if maxW > c.w {
			t.Errorf("%dx%d: max col %d (overflows by %d)", c.w, c.h, maxW, maxW-c.w)
		}
		if !t.Failed() {
			t.Logf("%dx%d → %d lines × %d cols (fits)", c.w, c.h, len(lines), maxW)
		}
	}
	_ = fmt.Sprintf
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

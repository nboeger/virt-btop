package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

// Style is a single cell's visual attributes. A nil foreground/background means
// the terminal default.
type Style struct {
	FG    termenv.Color
	BG    termenv.Color
	Bold  bool
	Faint bool
}

type Cell struct {
	R rune
	S Style
}

// Screen is a width x height cell buffer. Building the whole frame and then
// emitting it avoids the cursor bookkeeping and partial-update artifacts that a
// string-concatenation TUI would have.
type Screen struct {
	W, H  int
	cells []Cell
	pal   *Palette
}

func NewScreen(w, h int, pal *Palette) *Screen {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	s := &Screen{W: w, H: h, cells: make([]Cell, w*h), pal: pal}
	s.Fill(0, 0, w, h, Style{})
	return s
}

func (s *Screen) idx(x, y int) int { return y*s.W + x }

func (s *Screen) Set(x, y int, r rune, st Style) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	if r == 0 {
		r = ' '
	}
	s.cells[s.idx(x, y)] = Cell{R: r, S: st}
}

func (s *Screen) Fill(x, y, w, h int, st Style) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			s.Set(xx, yy, ' ', st)
		}
	}
}

// sanitize replaces every rune that is not exactly one terminal cell wide
// (wide CJK/emoji, zero-width marks, and control characters such as ESC or
// newline) with '?'. This keeps the cell grid exact and means text from outside
// the program, like VM names or libvirt error messages, can never inject
// terminal escape sequences.
func sanitize(text string) string {
	clean := true
	for _, r := range text {
		if runewidth.RuneWidth(r) != 1 {
			clean = false
			break
		}
	}
	if clean {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if runewidth.RuneWidth(r) != 1 {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Put writes text clipped to maxW cells; see sanitize for how unusual runes are
// handled.
func (s *Screen) Put(x, y, maxW int, text string, st Style) int {
	if maxW <= 0 {
		return x
	}
	start := x
	for _, r := range sanitize(text) {
		if x-start >= maxW {
			break
		}
		s.Set(x, y, r, st)
		x++
	}
	return x
}

func (s *Screen) PutRight(x, y, w int, text string, st Style) {
	text = runewidth.Truncate(sanitize(text), w, "")
	pad := w - runewidth.StringWidth(text)
	if pad < 0 {
		pad = 0
	}
	s.Put(x+pad, y, w-pad, text, st)
}

// Box fills a box, draws single-line borders, and overlays a title on the
// top-left border and a subtitle on the top-right border.
func (s *Screen) Box(x, y, w, h int, title, subtitle string, border, titleColor, subtitleColor termenv.Color) {
	if w < 2 || h < 2 {
		return
	}
	s.Fill(x, y, w, h, Style{})
	bs := Style{FG: border}

	for xx := x + 1; xx < x+w-1; xx++ {
		s.Set(xx, y, '─', bs)
		s.Set(xx, y+h-1, '─', bs)
	}
	for yy := y + 1; yy < y+h-1; yy++ {
		s.Set(x, yy, '│', bs)
		s.Set(x+w-1, yy, '│', bs)
	}
	s.Set(x, y, '╭', bs)
	s.Set(x+w-1, y, '╮', bs)
	s.Set(x, y+h-1, '╰', bs)
	s.Set(x+w-1, y+h-1, '╯', bs)

	titleText := ""
	if title != "" {
		titleText = " " + sanitize(title) + " "
		titleText = runewidth.Truncate(titleText, w-4, "…")
		s.Put(x+2, y, w-4, titleText, Style{FG: titleColor, Bold: true})
	}
	if subtitle != "" && w > 12 {
		sub := " " + sanitize(subtitle) + " "
		subW := runewidth.StringWidth(sub)
		start := x + w - 1 - subW
		titleEnd := x + 2 + runewidth.StringWidth(titleText)
		if start > titleEnd+1 {
			sub = runewidth.Truncate(sub, w-4, "…")
			s.Put(start, y, w-4, sub, Style{FG: subtitleColor})
		}
	}
}

func styleSeq(st Style) string {
	var b strings.Builder
	b.WriteString("\x1b[0m")
	if st.Bold {
		b.WriteString("\x1b[1m")
	}
	if st.Faint {
		b.WriteString("\x1b[2m")
	}
	if st.FG != nil {
		b.WriteString("\x1b[")
		b.WriteString(st.FG.Sequence(false))
		b.WriteByte('m')
	}
	if st.BG != nil {
		b.WriteString("\x1b[")
		b.WriteString(st.BG.Sequence(true))
		b.WriteByte('m')
	}
	return b.String()
}

// Render returns the full frame as ANSI text. Every line is exactly W cells.
func (s *Screen) Render() string {
	var out strings.Builder
	out.Grow(s.W * s.H * 2)
	for y := 0; y < s.H; y++ {
		cur := Style{}
		styled := false
		for x := 0; x < s.W; x++ {
			cell := s.cells[s.idx(x, y)]
			if cell.S != cur {
				out.WriteString(styleSeq(cell.S))
				cur = cell.S
				styled = true
			}
			r := cell.R
			if r == 0 {
				r = ' '
			}
			out.WriteRune(r)
		}
		if styled {
			out.WriteString("\x1b[0m")
		}
		if y < s.H-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

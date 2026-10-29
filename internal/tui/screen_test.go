package tui

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestScreenRenderExactWidth(t *testing.T) {
	pal := newPalette(termenv.Ascii)
	s := NewScreen(12, 3, pal)
	s.Box(0, 0, 12, 3, "t", "", pal.DivLine, pal.Title, pal.GraphText)
	s.Put(1, 1, 20, "hello", Style{FG: pal.MainFG})
	out := s.Render()
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, w := range lineWidths(out) {
		if w != 12 {
			t.Fatalf("line %d width = %d, want 12: %q", i, w, stripANSI(lines[i]))
		}
	}
	if !strings.Contains(stripANSI(lines[0]), "t") {
		t.Fatalf("title missing: %q", stripANSI(lines[0]))
	}
}

func TestScreenTrueColorBackground(t *testing.T) {
	pal := newPalette(termenv.TrueColor)
	s := NewScreen(2, 1, pal)
	s.Set(0, 0, 'x', Style{FG: pal.MainFG, BG: pal.SelectedBG})
	out := s.Render()
	if !strings.Contains(out, "38;2;204;204;204") {
		t.Fatalf("main fg sequence missing: %q", out)
	}
	if !strings.Contains(out, "48;2;105;47;47") {
		t.Fatalf("selected bg sequence missing: %q", out)
	}
}

func TestDrawBraille(t *testing.T) {
	pal := newPalette(termenv.Ascii)
	s := NewScreen(20, 4, pal)
	vals := make([]float64, 40)
	for i := range vals {
		vals[i] = 50
	}
	DrawBraille(s, 0, 0, 20, 4, vals, 100, func(f float64) termenv.Color { return pal.CPU.At(f) })
	count := 0
	for _, cell := range s.cells {
		if cell.R >= 0x2800 && cell.R <= 0x28FF {
			count++
		}
	}
	if count == 0 {
		t.Fatal("no braille cells drawn")
	}

	// All-zero values still draw a baseline rather than an empty graph.
	s2 := NewScreen(10, 3, pal)
	DrawBraille(s2, 0, 0, 10, 3, make([]float64, 20), 100, func(f float64) termenv.Color { return pal.CPU.At(f) })
	count = 0
	for _, cell := range s2.cells {
		if cell.R >= 0x2800 && cell.R <= 0x28FF {
			count++
		}
	}
	if count == 0 {
		t.Fatal("zero graph should still draw a baseline")
	}

	// A 100% plateau should fill every cell, not just draw an outline.
	s3 := NewScreen(10, 4, pal)
	full := make([]float64, 20)
	for i := range full {
		full[i] = 100
	}
	DrawBraille(s3, 0, 0, 10, 4, full, 100, func(f float64) termenv.Color { return pal.CPU.At(f) })
	nonSpace := 0
	for _, cell := range s3.cells {
		if cell.R != ' ' {
			nonSpace++
		}
	}
	if nonSpace != 40 {
		t.Fatalf("100%% graph should fill all 40 cells, got %d", nonSpace)
	}
}

func TestDrawBar(t *testing.T) {
	pal := newPalette(termenv.Ascii)
	s := NewScreen(10, 1, pal)
	drawBar(s, 0, 0, 10, 0.5, pal.Mem, nil, pal)
	// Half full: 5 full cells, 5 empty, still braille (not block) glyphs.
	for i := 0; i < 5; i++ {
		if c := s.cells[i]; c.R != 0x28FF {
			t.Fatalf("cell %d = %U, want full braille", i, c.R)
		}
	}
	for i := 5; i < 10; i++ {
		if c := s.cells[i]; c.R != 0x2800 {
			t.Fatalf("cell %d = %U, want empty braille", i, c.R)
		}
	}
	// A single sub-cell of progress still shows a dot.
	s2 := NewScreen(10, 1, pal)
	drawBar(s2, 0, 0, 10, 0.01, pal.Mem, nil, pal)
	if c := s2.cells[0]; c.R == 0x2800 {
		t.Fatal("tiny fraction should still draw a dot")
	}
}

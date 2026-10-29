package tui

import (
	"math"

	"github.com/muesli/termenv"
)

// DrawBraille renders a filled area graph into a w x h cell area using Unicode
// braille (U+2800-U+28FF), giving 2x4 sub-cell resolution. The area under the
// sampled value is filled down to the baseline.
func DrawBraille(s *Screen, x, y, w, h int, values []float64, maxVal float64, colorAt func(float64) termenv.Color) {
	drawBraille(s, x, y, w, h, values, maxVal, colorAt, false)
}

// DrawBrailleDown is like DrawBraille but mirrored vertically: the baseline is
// the top edge and the graph grows downward.
func DrawBrailleDown(s *Screen, x, y, w, h int, values []float64, maxVal float64, colorAt func(float64) termenv.Color) {
	drawBraille(s, x, y, w, h, values, maxVal, colorAt, true)
}

func drawBraille(s *Screen, x, y, w, h int, values []float64, maxVal float64, colorAt func(float64) termenv.Color, flip bool) {
	if w <= 0 || h <= 0 || s == nil {
		return
	}

	n := w * 2
	vals := make([]float64, n)
	if len(values) >= n {
		copy(vals, values[len(values)-n:])
	} else {
		copy(vals[n-len(values):], values)
	}

	if maxVal <= 0 {
		m := 0.0
		for _, v := range vals {
			if v > m {
				m = v
			}
		}
		maxVal = niceMax(m * 1.05)
	}
	if maxVal <= 0 {
		maxVal = 1
	}

	masks := make([]uint8, w*h)
	colors := make([]termenv.Color, w*h)
	maxY := h*4 - 1

	for i := 0; i < n; i++ {
		frac := clamp(vals[i]/maxVal, 0, 1)
		yd := int(math.Round((1 - frac) * float64(maxY)))
		if yd < 0 {
			yd = 0
		}
		if yd > maxY {
			yd = maxY
		}

		if flip {
			yd = maxY - yd // distance from the top baseline
		}
		cx := i / 2
		sx := i % 2
		color := colorAt(frac)
		// Fill from the sampled value down to the baseline.
		lo, hi := yd, maxY
		if flip {
			lo, hi = 0, yd
		}
		for yy := lo; yy <= hi; yy++ {
			cy := yy / 4
			sy := yy % 4
			if cx < 0 || cx >= w || cy < 0 || cy >= h {
				continue
			}
			idx := cy*w + cx
			masks[idx] |= brailleBit(sx, sy)
			colors[idx] = color
		}
	}

	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < w; cx++ {
			idx := cy*w + cx
			if masks[idx] == 0 {
				continue
			}
			s.Set(x+cx, y+cy, 0x2800+rune(masks[idx]), Style{FG: colors[idx]})
		}
	}
}

func brailleBit(sx, sy int) uint8 {
	switch {
	case sx == 0 && sy == 0:
		return 0x01
	case sx == 0 && sy == 1:
		return 0x02
	case sx == 0 && sy == 2:
		return 0x04
	case sx == 1 && sy == 0:
		return 0x08
	case sx == 1 && sy == 1:
		return 0x10
	case sx == 1 && sy == 2:
		return 0x20
	case sx == 0 && sy == 3:
		return 0x40
	case sx == 1 && sy == 3:
		return 0x80
	default:
		return 0
	}
}

func maxOf(series ...[]float64) float64 {
	m := 0.0
	for _, values := range series {
		for _, v := range values {
			if v > m {
				m = v
			}
		}
	}
	return m
}

package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/muesli/termenv"
)

// Palette mirrors btop's built-in default theme (muted greens, olives,
// purples, and reds) rather than a bright neon dashboard.
// graphBrightness brightens the memory and network gradients 30% over btop's
// defaults; btop's low-end stops are very dark on most terminal backgrounds.
const graphBrightness = 1.3

type Palette struct {
	Profile termenv.Profile

	MainFG     termenv.Color
	Title      termenv.Color
	HiFG       termenv.Color
	SelectedBG termenv.Color
	SelectedFG termenv.Color
	InactiveFG termenv.Color
	GraphText  termenv.Color
	MeterBG    termenv.Color
	ProcMisc   termenv.Color

	BorderHeader termenv.Color
	BorderCPU    termenv.Color
	BorderMem    termenv.Color
	BorderNet    termenv.Color
	BorderVMs    termenv.Color
	DivLine      termenv.Color
	Error        termenv.Color

	CPU      Gradient
	Mem      Gradient
	Free     Gradient
	Avail    Gradient
	Download Gradient
	Upload   Gradient
	Proc     Gradient
	// Stale is the muted gradient for values that may be out of date.
	Stale Gradient

	StateRunning   termenv.Color
	StateBlocked   termenv.Color
	StatePaused    termenv.Color
	StateShutdown  termenv.Color
	StateShutoff   termenv.Color
	StateCrashed   termenv.Color
	StateSuspended termenv.Color
}

type Gradient struct {
	colors []termenv.Color
}

func (g Gradient) At(f float64) termenv.Color {
	if len(g.colors) == 0 {
		return nil
	}
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	idx := int(f*float64(len(g.colors)-1) + 0.5)
	return g.colors[idx]
}

func newGradient(p termenv.Profile, stops ...string) Gradient {
	return newGradientScaled(p, 1, stops...)
}

// newGradientScaled builds a gradient with every stop's RGB multiplied by
// scale (clamped to 255). A scale of 1.3 is 30% brighter.
func newGradientScaled(p termenv.Profile, scale float64, stops ...string) Gradient {
	const steps = 64
	scaled := make([]string, len(stops))
	for i, s := range stops {
		scaled[i] = scaleHex(s, scale)
	}
	out := make([]termenv.Color, steps)
	for i := 0; i < steps; i++ {
		f := float64(i) / float64(steps-1)
		out[i] = p.Color(lerpHex(scaled, f))
	}
	return Gradient{colors: out}
}

// scaleHex brightens a color by multiplying its brightness (mean RGB) by scale.
// Plain per-channel multiplication would clip bright channels at 255 and lose
// part of the gain while shifting the hue, so whatever a channel cannot take is
// spread over the channels that still have headroom (the color gets paler
// instead). White stays white.
func scaleHex(hex string, scale float64) string {
	r, g, b := hexRGB(hex)
	ch := [3]float64{float64(r) * scale, float64(g) * scale, float64(b) * scale}

	var excess float64
	for i, v := range ch {
		if v > 255 {
			excess += v - 255
			ch[i] = 255
		}
	}
	if excess > 0 {
		var headroom float64
		for _, v := range ch {
			headroom += 255 - v
		}
		if headroom > 0 {
			add := excess
			if add > headroom {
				add = headroom
			}
			for i, v := range ch {
				ch[i] = v + add*(255-v)/headroom
			}
		}
	}
	c := func(v float64) int {
		n := int(v + 0.5)
		if n > 255 {
			return 255
		}
		return n
	}
	return fmt.Sprintf("#%02x%02x%02x", c(ch[0]), c(ch[1]), c(ch[2]))
}

func lerpHex(stops []string, f float64) string {
	if len(stops) == 0 {
		return "#000000"
	}
	if len(stops) == 1 {
		return stops[0]
	}
	pos := f * float64(len(stops)-1)
	i := int(pos)
	if i >= len(stops)-1 {
		i = len(stops) - 2
	}
	t := pos - float64(i)
	return mixHex(stops[i], stops[i+1], t)
}

func mixHex(a, b string, t float64) string {
	ar, ag, ab := hexRGB(a)
	br, bg, bb := hexRGB(b)
	r := int(float64(ar) + (float64(br)-float64(ar))*t)
	g := int(float64(ag) + (float64(bg)-float64(ag))*t)
	bl := int(float64(ab) + (float64(bb)-float64(ab))*t)
	return fmt.Sprintf("#%02x%02x%02x", r, g, bl)
}

func hexRGB(s string) (int, int, int) {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}

func NewPalette() *Palette {
	return newPalette(termenv.EnvColorProfile())
}

func newPalette(p termenv.Profile) *Palette {
	c := func(hex string) termenv.Color { return p.Color(hex) }
	return &Palette{
		Profile: p,

		MainFG:     c("#cccccc"),
		Title:      c("#eeeeee"),
		HiFG:       c("#b54040"),
		SelectedBG: c("#6a2f2f"),
		SelectedFG: c("#eeeeee"),
		InactiveFG: c("#404040"),
		GraphText:  c("#606060"),
		MeterBG:    c("#404040"),
		ProcMisc:   c("#0de756"),

		BorderHeader: c("#66a682"),
		BorderCPU:    c("#a29a56"),
		BorderMem:    c("#66a682"),
		BorderNet:    c("#7c8faa"),
		BorderVMs:    c("#a67272"),
		DivLine:      c("#303030"),
		Error:        c("#dc4c4c"),

		CPU:      newGradient(p, "#77ca9b", "#cbc06c", "#dc4c4c"),
		Mem:      newGradientScaled(p, graphBrightness, "#592b26", "#d9626d", "#ff4769"),
		Free:     newGradient(p, "#384f21", "#b5e685", "#dcff85"),
		Avail:    newGradient(p, "#4e3f0e", "#ffd77a", "#ffb814"),
		Download: newGradientScaled(p, graphBrightness, "#291f75", "#4f43a3", "#b0a9de"),
		Upload:   newGradientScaled(p, graphBrightness, "#620665", "#7d4180", "#dcafde"),
		Proc:     newGradient(p, "#80d0a3", "#dcd179", "#d45454"),
		Stale:    newGradient(p, "#404040", "#5a5a5a", "#707070"),

		StateRunning:   c("#77ca9b"),
		StateBlocked:   c("#cbc06c"),
		StatePaused:    c("#ffd77a"),
		StateShutdown:  c("#b0a9de"),
		StateShutoff:   c("#606060"),
		StateCrashed:   c("#dc4c4c"),
		StateSuspended: c("#74e6fc"),
	}
}

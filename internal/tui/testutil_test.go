package tui

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func visibleLines(view string) []string {
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] = stripANSI(lines[i])
	}
	return lines
}

func lineWidths(view string) []int {
	lines := visibleLines(view)
	out := make([]int, len(lines))
	for i, line := range lines {
		out[i] = runewidth.StringWidth(line)
	}
	return out
}

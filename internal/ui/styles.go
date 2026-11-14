// Package ui renders mdc's interactive output: shared styles and the live
// progress view.
package ui

import (
	"hash/fnv"

	"charm.land/lipgloss/v2"
)

// Shared styles. lipgloss downgrades or strips colour for the output
// terminal (including NO_COLOR) when rendering through its writers.
var (
	Header = lipgloss.NewStyle().Bold(true)
	OK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	Warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	Err    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	Dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// namePalette avoids black, white, red and the dim grey so prefixes stay
// distinguishable from status colours.
var namePalette = []string{"4", "5", "6", "12", "13", "14", "33", "99", "135", "172", "208", "37"}

// NameStyle returns a style whose colour is stable for name, for prefixing
// log lines by project or service.
func NameStyle(name string) lipgloss.Style {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return lipgloss.NewStyle().Foreground(lipgloss.Color(namePalette[h.Sum32()%uint32(len(namePalette))]))
}

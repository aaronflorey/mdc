package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/aaronflorey/mdc/internal/discover"
)

// ProjectTitle is the project name, followed by its directory when that
// differs from the name.
func ProjectTitle(t discover.Target) string {
	title := Header.Render(t.ProjectName)
	if t.Label != t.ProjectName {
		title += " " + Dim.Render(t.Label)
	}
	return title
}

// SkippedLine is the single dim line shown for a project that has nothing to do.
func SkippedLine(t discover.Target, reason string) string {
	title := t.ProjectName
	if t.Label != t.ProjectName {
		title += " " + t.Label
	}
	return Dim.Render("○ " + title + " " + reason)
}

// Section is a project's title above its body.
func Section(t discover.Target, body string) string {
	return ProjectTitle(t) + "\n" + body
}

// Failure is a project's title above its full error text.
func Failure(t discover.Target, err error) string {
	return Err.Render("✗ ") + ProjectTitle(t) + "\n" + indent(strings.TrimSpace(err.Error()))
}

// TerminalWidth is the width of the output terminal, or 80 when unknown.
func TerminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// StateStyle colours a container state, or its health when that is worse
// than the state suggests.
func StateStyle(state, health string) Style {
	switch {
	case health == "unhealthy":
		return Err
	case health == "starting":
		return Warn
	}
	switch state {
	case "running":
		return OK
	case "exited", "dead":
		return Err
	case "paused", "restarting", "removing":
		return Warn
	default:
		return Dim
	}
}

func truncate(s string, width int) string { return ansi.Truncate(s, width, "…") }

package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/docker/compose/v5/pkg/api"

	"github.com/aaronflorey/mdc/internal/runner"
)

const maxBarWidth = 30

// section is the rendered lines of one project.
type section struct {
	header string
	// rows are the project's body lines; priority marks rows worth keeping
	// when space is short.
	rows     []string
	priority []bool
	running  bool
}

func (m *model) render() string {
	footer := m.footer()
	var sections []section
	for _, p := range m.projects {
		sections = append(sections, m.section(p))
	}
	lines := fit(sections, max(m.height-len(footer), 0))
	lines = append(lines, footer...)

	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

func (m *model) section(p *projectState) section {
	header := projectTitle(p)
	if !p.running() {
		return section{header: outcomeLine(p)}
	}
	s := section{header: header, running: true}
	for _, r := range p.rows() {
		s.rows = append(s.rows, m.rowLine(r))
		s.priority = append(s.priority, r.active())
	}
	return s
}

func projectTitle(p *projectState) string {
	return ProjectTitle(p.target)
}

func outcomeLine(p *projectState) string {
	switch o := p.outcome; o.Status {
	case runner.OK:
		line := OK.Render("✓") + " " + projectTitle(p)
		if o.Detail != "" {
			line += " " + Dim.Render(o.Detail)
		}
		return line
	case runner.Skipped:
		return SkippedLine(p.target, o.Reason)
	default:
		return Err.Render("✗") + " " + projectTitle(p) + " " + Err.Render(firstLine(o.Err.Error()))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func (m *model) icon(s api.EventStatus) string {
	switch s {
	case api.Done:
		return OK.Render("✓")
	case api.Warning:
		return Warn.Render("!")
	case api.Error:
		return Err.Render("✗")
	default:
		return m.spinner.View()
	}
}

func (m *model) rowLine(r row) string {
	prefix := strings.Repeat("  ", r.Indent+1) + m.icon(r.Status) + " " + r.ID
	if r.Bar && r.Status == api.Working {
		width := min(maxBarWidth, m.width-lipgloss.Width(prefix)-2)
		if width < 10 {
			return prefix
		}
		m.bar.SetWidth(width)
		return prefix + " " + m.bar.ViewAs(r.Percent/100)
	}
	text := r.Text
	if r.Status == api.Error {
		text = Err.Render(text)
	} else if r.Status == api.Warning {
		text = Warn.Render(text)
	}
	if r.Details != "" {
		text += " " + Dim.Render(r.Details)
	}
	return prefix + " " + text
}

// fit lays sections out in at most height lines. Finished projects are one
// line; when the running ones do not fit, rows are dropped (finished rows
// first) and replaced by a "+N more" marker.
func fit(sections []section, height int) []string {
	limits := make([]int, len(sections))
	total := 0
	for i, s := range sections {
		limits[i] = len(s.rows)
		total += 1 + limits[i]
	}
	// Shrink the project showing the most rows until everything fits.
	for total > height {
		widest := -1
		for i, l := range limits {
			if l > 0 && (widest < 0 || l > limits[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break
		}
		limits[widest]--
		total--
	}

	var lines []string
	for i, s := range sections {
		lines = append(lines, s.header)
		lines = append(lines, s.visible(limits[i])...)
	}
	if len(lines) > height {
		// Even bare headers do not fit: keep running projects first.
		lines = headersOnly(sections, height)
	}
	return lines
}

// visible returns at most limit rows, keeping priority rows in order and
// ending with a count of what was hidden.
func (s section) visible(limit int) []string {
	if limit >= len(s.rows) {
		return s.rows
	}
	if limit <= 0 {
		return nil
	}
	keep := limit - 1 // room for the marker
	chosen := make([]bool, len(s.rows))
	for _, wantPriority := range []bool{true, false} {
		for i := range s.rows {
			if keep > 0 && !chosen[i] && s.priority[i] == wantPriority {
				chosen[i] = true
				keep--
			}
		}
	}
	var out []string
	hidden := 0
	for i, r := range s.rows {
		if chosen[i] {
			out = append(out, r)
		} else {
			hidden++
		}
	}
	return append(out, Dim.Render(fmt.Sprintf("  … %d more", hidden)))
}

func headersOnly(sections []section, height int) []string {
	var lines []string
	for _, running := range []bool{true, false} {
		for _, s := range sections {
			if s.running == running && len(lines) < height {
				lines = append(lines, s.header)
			}
		}
	}
	return lines
}

func (m *model) footer() []string {
	lines := []string{m.countsLine()}
	if m.prompt != nil {
		hint := "[y/N]"
		if m.prompt.def {
			hint = "[Y/n]"
		}
		name := m.projects[m.prompt.idx].target.ProjectName
		lines = append(lines, Warn.Render("?")+" "+Header.Render(name)+" "+m.prompt.message+" "+Dim.Render(hint))
	}
	if m.cancelling {
		lines = append(lines, Dim.Render("Cancelling…"))
	}
	return lines
}

func (m *model) countsLine() string {
	c := tally(m.projects)
	var parts []string
	if c.Running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", c.Running))
	}
	if c.OK > 0 {
		parts = append(parts, OK.Render(fmt.Sprintf("%d ok", c.OK)))
	}
	if c.Skipped > 0 {
		parts = append(parts, Dim.Render(fmt.Sprintf("%d skipped", c.Skipped)))
	}
	if c.Failed > 0 {
		parts = append(parts, Err.Render(fmt.Sprintf("%d failed", c.Failed)))
	}
	return strings.Join(parts, Dim.Render(" · "))
}

// summary is printed once the view exits: every project's result, the counts,
// and the full error text of each failure.
func (m *model) summary() string {
	var lines []string
	var failures []string
	for _, p := range m.projects {
		if p.running() {
			continue
		}
		lines = append(lines, outcomeLine(p))
		if p.outcome.Status == runner.Failed {
			failures = append(failures, Failure(p.target, p.outcome.Err))
		}
	}
	lines = append(lines, "", m.countsLine())
	if len(failures) > 0 {
		lines = append(lines, "", strings.Join(failures, "\n"))
	}
	return strings.Join(lines, "\n")
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

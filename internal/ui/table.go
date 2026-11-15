package ui

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

// Style is a lipgloss style.
type Style = lipgloss.Style

const (
	columnGap = 2
	minColumn = 6
)

// Column describes one table column.
type Column struct {
	Title string
	// Drop orders which columns go first when the table is too wide: higher
	// values are dropped first and 0 is never dropped.
	Drop int
}

// RenderTable lays out rows (cells may contain styling) in at most width
// columns. Low-value columns are dropped, then the widest remaining ones are
// truncated, so rows never wrap.
func RenderTable(cols []Column, rows [][]string, width int) string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = ansi.StringWidth(c.Title)
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], ansi.StringWidth(cell))
		}
	}

	keep := keptColumns(cols, widths, width)
	kept := make([]int, len(keep))
	titles := make([]string, len(keep))
	for n, i := range keep {
		kept[n] = widths[i]
		titles[n] = cols[i].Title
	}
	shrink(kept, width-columnGap*(len(kept)-1))

	t := table.New().
		Border(lipgloss.Border{}).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderHeader(false).
		Wrap(false).
		Headers(titles...).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := lipgloss.NewStyle().Width(kept[col] + columnGap).PaddingRight(columnGap)
			if col == len(kept)-1 {
				style = lipgloss.NewStyle().Width(kept[col])
			}
			if row == table.HeaderRow {
				style = style.Bold(true)
			}
			return style
		})
	for _, row := range rows {
		cells := make([]string, len(keep))
		for n, i := range keep {
			cells[n] = truncate(row[i], kept[n])
		}
		t.Row(cells...)
	}
	return strings.TrimRight(t.String(), "\n")
}

// keptColumns returns the indexes of the columns that remain once the
// droppable ones, highest Drop first, are removed until the table fits width.
func keptColumns(cols []Column, widths []int, width int) []int {
	keep := make([]int, len(cols))
	for i := range keep {
		keep[i] = i
	}
	total := func() int {
		sum := columnGap * (len(keep) - 1)
		for _, i := range keep {
			sum += widths[i]
		}
		return sum
	}
	for total() > width {
		victim := -1
		for n, i := range keep {
			if cols[i].Drop > 0 && (victim < 0 || cols[i].Drop > cols[keep[victim]].Drop) {
				victim = n
			}
		}
		if victim < 0 {
			break
		}
		keep = slices.Delete(keep, victim, victim+1)
	}
	return keep
}

// shrink narrows the widest columns until they sum to at most avail.
func shrink(widths []int, avail int) {
	sum := 0
	for _, w := range widths {
		sum += w
	}
	for sum > avail {
		widest := 0
		for i, w := range widths {
			if w > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= minColumn {
			return
		}
		widths[widest]--
		sum--
	}
}

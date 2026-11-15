package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestKeptColumnsDropsHighestDropFirstAndNeverZero(t *testing.T) {
	cols := []Column{{Title: "A"}, {Title: "B", Drop: 1}, {Title: "C", Drop: 2}, {Title: "D"}}
	widths := []int{10, 10, 10, 10}

	if got := keptColumns(cols, widths, 100); len(got) != 4 {
		t.Fatalf("everything fits, got %v", got)
	}
	if got := keptColumns(cols, widths, 36); !slices.Equal(got, []int{0, 1, 3}) {
		t.Fatalf("C goes first, got %v", got)
	}
	if got := keptColumns(cols, widths, 5); !slices.Equal(got, []int{0, 3}) {
		t.Fatalf("columns without a Drop value stay, got %v", got)
	}
}

func TestRenderTableNeverExceedsWidth(t *testing.T) {
	cols := []Column{{Title: "NAME"}, {Title: "IMAGE", Drop: 1}, {Title: "STATUS"}}
	rows := [][]string{{"a-very-long-container-name", "registry.example.com/team/app:latest", "Up 3 minutes"}}
	for _, width := range []int{20, 40, 60, 120} {
		for _, line := range strings.Split(RenderTable(cols, rows, width), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("width %d: line is %d wide: %q", width, w, line)
			}
		}
	}
}

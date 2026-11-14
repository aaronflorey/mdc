package cli

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newTopCommand) }

func newTopCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "top [SERVICES...]",
		Short: "Display the running processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			snaps, err := collect(cmd.Context(), app, runOptions{Services: args}, func(ctx context.Context, r projectRun) ([]api.ContainerProcSummary, error) {
				return r.Compose.Top(ctx, r.Project.Name, r.Services)
			})
			if err != nil {
				return err
			}
			for _, s := range succeeded(snaps) {
				body := ui.Dim.Render("no running processes")
				if cols, rows := topRows(s.Value); len(rows) > 0 {
					body = ui.RenderTable(cols, rows, ui.TerminalWidth())
				}
				printLine(os.Stdout, ui.Section(s.Target, body))
			}
			return reportOutcomes(outcomesOf(snaps), true)
		},
	}
}

// topRows builds one table from containers whose process columns may differ:
// SERVICE and # first, then each title in order of appearance, with CMD last.
func topRows(containers []api.ContainerProcSummary) ([]ui.Column, [][]string) {
	containers = slices.Clone(containers)
	slices.SortFunc(containers, func(a, b api.ContainerProcSummary) int { return strings.Compare(a.Name, b.Name) })

	titles := []string{"SERVICE", "#"}
	for _, c := range containers {
		for _, title := range c.Titles {
			if !slices.Contains(titles, title) {
				titles = append(titles, title)
			}
		}
	}
	if i := slices.Index(titles, "CMD"); i >= 0 {
		titles = append(slices.Delete(titles, i, i+1), "CMD")
	}

	var rows [][]string
	for _, c := range containers {
		for _, proc := range c.Processes {
			values := map[string]string{"SERVICE": c.Service, "#": c.Replica}
			for i, title := range c.Titles {
				values[title] = proc[i]
			}
			row := make([]string, len(titles))
			for i, title := range titles {
				row[i] = "-"
				if v, ok := values[title]; ok {
					row[i] = v
				}
			}
			rows = append(rows, row)
		}
	}
	cols := make([]ui.Column, len(titles))
	for i, title := range titles {
		cols[i] = ui.Column{Title: title}
	}
	return cols, rows
}

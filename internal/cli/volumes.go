package cli

import (
	"context"
	"os"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newVolumesCommand) }

func newVolumesCommand(app *App) *cobra.Command {
	var format string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "volumes [OPTIONS] [SERVICE...]",
		Short: "List volumes",
		PreRunE: func(*cobra.Command, []string) error {
			return outputFormat(format, "table", "json")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			snaps, err := collect(cmd.Context(), app, runOptions{Services: args}, func(ctx context.Context, r projectRun) ([]api.VolumesSummary, error) {
				return r.Compose.Volumes(ctx, r.Project.Name, api.VolumesOptions{Services: r.Services})
			})
			if err != nil {
				return err
			}
			rows := merge(succeeded(snaps), func(v api.VolumesSummary) string { return v.Name })

			switch {
			case quiet:
				for _, row := range rows {
					printLine(os.Stdout, row.Item.Name)
				}
			case format == "json":
				list := make([]api.VolumesSummary, len(rows))
				for i, row := range rows {
					list[i] = row.Item
				}
				if err := printJSON(list); err != nil {
					return err
				}
			case len(rows) > 0:
				cols := []ui.Column{{Title: "PROJECT"}, {Title: "DRIVER", Drop: 1}, {Title: "VOLUME NAME"}}
				cells := make([][]string, len(rows))
				for i, row := range rows {
					cells[i] = []string{projectCell(row.Project), row.Item.Driver, row.Item.Name}
				}
				printLine(os.Stdout, ui.RenderTable(cols, cells, ui.TerminalWidth()))
			}
			return reportOutcomes(outcomesOf(snaps), format != "json" && !quiet)
		},
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only display volume names")
	cmd.Flags().StringVar(&format, "format", "table", "Format the output. Values: [table | json]")
	return cmd
}

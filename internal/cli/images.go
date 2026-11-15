package cli

import (
	"context"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/containerd/platforms"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/go-units"
	"github.com/moby/moby/client/pkg/stringid"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newImagesCommand) }

// imageRow is an image together with the container that uses it.
type imageRow struct {
	Container string
	api.ImageSummary
}

func newImagesCommand(app *App) *cobra.Command {
	var format string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "images [OPTIONS] [SERVICE...]",
		Short: "List images used by the created containers",
		PreRunE: func(*cobra.Command, []string) error {
			return outputFormat(format, "table", "json")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			snaps, err := collect(cmd.Context(), app, runOptions{Services: args}, func(ctx context.Context, r projectRun) ([]imageRow, error) {
				images, err := r.Compose.Images(ctx, r.Project.Name, api.ImagesOptions{Services: r.Services})
				if err != nil {
					return nil, err
				}
				var rows []imageRow
				for _, name := range slices.Sorted(maps.Keys(images)) {
					rows = append(rows, imageRow{Container: name, ImageSummary: images[name]})
				}
				return rows, nil
			})
			if err != nil {
				return err
			}
			rows := merge(succeeded(snaps), func(i imageRow) string { return i.Container })

			switch {
			case quiet:
				var seen []string
				for _, row := range rows {
					_, id, found := strings.Cut(row.Item.ID, ":")
					if !found {
						id = row.Item.ID
					}
					if !slices.Contains(seen, id) {
						seen = append(seen, id)
						printLine(os.Stdout, id)
					}
				}
			case format == "json":
				if err := printJSON(imagesJSON(rows)); err != nil {
					return err
				}
			case len(rows) > 0:
				printLine(os.Stdout, imagesTable(rows))
			}
			return reportOutcomes(outcomesOf(snaps), format != "json" && !quiet)
		},
	}
	cmd.Flags().StringVar(&format, "format", "table", "Format the output. Values: [table | json]")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only display IDs")
	return cmd
}

func imagesTable(rows []projectItem[imageRow]) string {
	cols := []ui.Column{
		{Title: "PROJECT"}, {Title: "CONTAINER"}, {Title: "REPOSITORY"}, {Title: "TAG"},
		{Title: "PLATFORM", Drop: 3}, {Title: "IMAGE ID", Drop: 2}, {Title: "SIZE"}, {Title: "CREATED", Drop: 4},
	}
	cells := make([][]string, len(rows))
	for i, row := range rows {
		img := row.Item
		created := "N/A"
		if img.Created != nil {
			created = units.HumanDuration(time.Now().UTC().Sub(*img.Created)) + " ago"
		}
		cells[i] = []string{
			projectCell(row.Project), img.Container, orNone(img.Repository), orNone(img.Tag),
			platforms.Format(img.Platform), stringid.TruncateID(img.ID),
			units.HumanSizeWithPrecision(float64(img.Size), 3), created,
		}
	}
	return ui.RenderTable(cols, cells, ui.TerminalWidth())
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}

func imagesJSON(rows []projectItem[imageRow]) []any {
	type image struct {
		Project       string     `json:"Project"`
		ID            string     `json:"ID"`
		ContainerName string     `json:"ContainerName"`
		Repository    string     `json:"Repository"`
		Tag           string     `json:"Tag"`
		Platform      string     `json:"Platform"`
		Size          int64      `json:"Size"`
		Created       *time.Time `json:"Created,omitempty"`
		LastTagTime   time.Time  `json:"LastTagTime,omitzero"`
	}
	list := make([]any, len(rows))
	for i, row := range rows {
		img := row.Item
		list[i] = image{
			Project: row.Project, ID: img.ID, ContainerName: img.Container, Repository: img.Repository, Tag: img.Tag,
			Platform: platforms.Format(img.Platform), Size: img.Size, Created: img.Created, LastTagTime: img.LastTagTime,
		}
	}
	return list
}

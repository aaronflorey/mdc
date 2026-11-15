package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"
)

func init() { Register(newEventsCommand) }

func newEventsCommand(app *App) *cobra.Command {
	var (
		asJSON       bool
		since, until string
	)
	cmd := &cobra.Command{
		Use:   "events [OPTIONS] [SERVICE...]",
		Short: "Receive real time events from containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			stream := newLogStream(true, true, false)
			return app.streamEach(cmd.Context(), runOptions{Services: args}, true, func(ctx context.Context, r projectRun) error {
				consumer := stream.Consumer(r.Project.Name, replicasOf(r.Project))
				return r.Compose.Events(ctx, r.Project.Name, api.EventsOptions{
					Services: r.Services,
					Since:    since,
					Until:    until,
					Consumer: func(event api.Event) error {
						if !asJSON {
							consumer.Log(event.Service, strings.TrimSuffix(event.String(), "\n"))
							return nil
						}
						line, err := json.Marshal(map[string]any{
							"time":       event.Timestamp,
							"type":       "container",
							"project":    r.Project.Name,
							"service":    event.Service,
							"id":         event.Container,
							"action":     event.Status,
							"attributes": event.Attributes,
						})
						if err != nil {
							return err
						}
						stream.Raw(string(line) + "\n")
						return nil
					},
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&asJSON, "json", false, "Output events as a stream of json objects")
	flags.StringVar(&since, "since", "", "Show all events created since timestamp")
	flags.StringVar(&until, "until", "", "Stream events until this timestamp")
	return cmd
}

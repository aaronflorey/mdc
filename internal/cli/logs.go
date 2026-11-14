package cli

import (
	"context"
	"errors"
	"slices"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"
)

func init() { Register(newLogsCommand) }

type logsOptions struct {
	follow     bool
	index      int
	tail       string
	since      string
	until      string
	noColor    bool
	noPrefix   bool
	timestamps bool
}

func newLogsCommand(app *App) *cobra.Command {
	var opts logsOptions
	cmd := &cobra.Command{
		Use:   "logs [OPTIONS] [SERVICE...]",
		Short: "View output from containers",
		PreRunE: func(_ *cobra.Command, args []string) error {
			if opts.index > 0 && len(args) != 1 {
				return errors.New("--index requires one service to be selected")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stream := newLogStream(!opts.noColor, !opts.noPrefix, false)
			return app.streamEach(cmd.Context(), runOptions{Services: args}, opts.follow, func(ctx context.Context, r projectRun) error {
				return r.Compose.Logs(ctx, r.Project.Name, stream.Consumer(r.Project.Name, replicasOf(r.Project)), api.LogOptions{
					Project:    r.Project,
					Services:   opts.services(r),
					Follow:     opts.follow,
					Index:      opts.index,
					Tail:       opts.tail,
					Since:      opts.since,
					Until:      opts.until,
					Timestamps: opts.timestamps,
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&opts.follow, "follow", "f", false, "Follow log output")
	flags.IntVar(&opts.index, "index", 0, "index of the container if service has multiple replicas")
	flags.StringVar(&opts.since, "since", "", "Show logs since timestamp (e.g. 2013-01-02T13:23:37Z) or relative (e.g. 42m for 42 minutes)")
	flags.StringVar(&opts.until, "until", "", "Show logs before a timestamp (e.g. 2013-01-02T13:23:37Z) or relative (e.g. 42m for 42 minutes)")
	flags.BoolVar(&opts.noColor, "no-color", false, "Produce monochrome output")
	flags.BoolVar(&opts.noPrefix, "no-log-prefix", false, "Don't print prefix in logs")
	flags.BoolVarP(&opts.timestamps, "timestamps", "t", false, "Show timestamps")
	flags.StringVarP(&opts.tail, "tail", "n", "all", "Number of lines to show from the end of the logs for each container")
	return cmd
}

// services are the services to show: the requested ones, or else every
// service that has not opted out of attaching.
func (logsOptions) services(r projectRun) []string {
	if len(r.Services) > 0 {
		return r.Services
	}
	var services []string
	for name, service := range r.Project.Services {
		if service.Attach == nil || *service.Attach {
			services = append(services, name)
		}
	}
	slices.Sort(services)
	return services
}

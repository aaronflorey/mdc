package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func init() { Register(newCreateCommand) }

func newCreateCommand(app *App) *cobra.Command {
	var opts createOptions
	cmd := &cobra.Command{
		Use:   "create [OPTIONS] [SERVICE...]",
		Short: "Creates containers for a service",
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			opts.pullChanged = cmd.Flags().Changed("pull")
			return opts.validate()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), opts.runOptions(args), func(ctx context.Context, r projectRun) error {
				if err := rejectScheduledJobs(r.Project); err != nil {
					return err
				}
				if err := opts.apply(r.Project); err != nil {
					return err
				}
				return r.Compose.Create(ctx, r.Project, opts.apiOptions(r.Project, r.Services, opts.buildOptions(r.Services, false)))
			})
		},
	}
	addCreateFlags(cmd.Flags(), &opts, `Pull image before running ("always"|"missing"|"never"|"build")`)
	return cmd
}

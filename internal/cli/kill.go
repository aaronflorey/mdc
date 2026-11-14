package cli

import (
	"context"
	"errors"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/runner"
)

func init() {
	Register(newKillCommand)
	Register(newPauseCommand)
	Register(newUnpauseCommand)
}

func newKillCommand(app *App) *cobra.Command {
	var (
		removeOrphans bool
		signal        string
	)
	cmd := &cobra.Command{
		Use:   "kill [OPTIONS] [SERVICE...]",
		Short: "Force stop service containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				err := r.Compose.Kill(ctx, r.Project.Name, api.KillOptions{
					RemoveOrphans: removeOrphans,
					Project:       r.Project,
					Services:      r.Services,
					Signal:        signal,
				})
				if errors.Is(err, api.ErrNoResources) {
					return runner.Note("no containers to kill")
				}
				return err
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&removeOrphans, "remove-orphans", envBool(envRemoveOrphans), "Remove containers for services not defined in the Compose file")
	flags.StringVarP(&signal, "signal", "s", "SIGKILL", "SIGNAL to send to the container")
	return cmd
}

func newPauseCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "pause [SERVICE...]",
		Short: "Pause services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				return r.Compose.Pause(ctx, r.Project.Name, api.PauseOptions{Services: r.Services, Project: r.Project})
			})
		},
	}
}

func newUnpauseCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "unpause [SERVICE...]",
		Short: "Unpause services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				return r.Compose.UnPause(ctx, r.Project.Name, api.PauseOptions{Services: r.Services, Project: r.Project})
			})
		},
	}
}

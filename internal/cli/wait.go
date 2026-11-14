package cli

import (
	"context"
	"fmt"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/runner"
)

func init() { Register(newWaitCommand) }

func newWaitCommand(app *App) *cobra.Command {
	var downProject bool
	cmd := &cobra.Command{
		Use:   "wait SERVICE [SERVICE...] [OPTIONS]",
		Short: "Block until containers of all (or specified) services stop.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				code, err := r.Compose.Wait(ctx, r.Project.Name, api.WaitOptions{
					Services:                   r.Services,
					DownProjectOnContainerExit: downProject,
				})
				if err != nil {
					return err
				}
				if code != 0 {
					return fmt.Errorf("exited with code %d", code)
				}
				return runner.Note("exit code 0")
			})
		},
	}
	cmd.Flags().BoolVar(&downProject, "down-project", false, "Drops project when the first container stops")
	return cmd
}

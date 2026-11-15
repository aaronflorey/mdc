package cli

import (
	"context"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"
)

func init() {
	Register(newStartCommand)
	Register(newStopCommand)
	Register(newRestartCommand)
}

func newStartCommand(app *App) *cobra.Command {
	var (
		wait    bool
		timeout int
	)
	cmd := &cobra.Command{
		Use:   "start [SERVICE...]",
		Short: "Start services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				if err := rejectScheduledJobs(r.Project); err != nil {
					return err
				}
				return r.Compose.Start(ctx, r.Project.Name, api.StartOptions{
					AttachTo:    r.Services,
					Project:     r.Project,
					Services:    r.Services,
					Wait:        wait,
					WaitTimeout: waitTimeout(timeout),
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&wait, "wait", false, "Wait for services to be running|healthy. Implies detached mode.")
	flags.IntVar(&timeout, "wait-timeout", 0, "Maximum duration in seconds to wait for the project to be running|healthy")
	return cmd
}

func newStopCommand(app *App) *cobra.Command {
	var timeout int
	cmd := &cobra.Command{
		Use:   "stop [OPTIONS] [SERVICE...]",
		Short: "Stop services",
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed("timeout")
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				return r.Compose.Stop(ctx, r.Project.Name, api.StopOptions{
					Timeout:  optionalTimeout(timeout, changed),
					Services: r.Services,
					Project:  r.Project,
				})
			})
		},
	}
	cmd.Flags().IntVarP(&timeout, "timeout", "t", 0, "Specify a shutdown timeout in seconds")
	return cmd
}

func newRestartCommand(app *App) *cobra.Command {
	var (
		timeout int
		noDeps  bool
	)
	cmd := &cobra.Command{
		Use:   "restart [OPTIONS] [SERVICE...]",
		Short: "Restart service containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed("timeout")
			return app.runEach(cmd.Context(), runOptions{Services: args, WholeProject: true}, func(ctx context.Context, r projectRun) error {
				return r.Compose.Restart(ctx, r.Project.Name, api.RestartOptions{
					Timeout:  optionalTimeout(timeout, changed),
					Services: r.Services,
					Project:  r.Project,
					NoDeps:   noDeps,
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.IntVarP(&timeout, "timeout", "t", 0, "Specify a shutdown timeout in seconds")
	flags.BoolVar(&noDeps, "no-deps", false, "Don't restart dependent services")
	return cmd
}

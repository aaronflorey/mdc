package cli

import (
	"context"
	"errors"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/runner"
)

func init() { Register(newWatchCommand) }

type watchOptions struct {
	quiet bool
	prune bool
	noUp  bool
}

func newWatchCommand(app *App) *cobra.Command {
	var opts watchOptions
	cmd := &cobra.Command{
		Use:   "watch [SERVICE...]",
		Short: "Watch build context for service and rebuild/refresh containers when files are updated",
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runAll(cmd.Context(), app, args)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.quiet, "quiet", false, "hide build output")
	flags.BoolVar(&opts.prune, "prune", true, "Prune dangling images on rebuild")
	flags.BoolVar(&opts.noUp, "no-up", false, "Do not build & start services before watching")
	return cmd
}

func (o watchOptions) runAll(ctx context.Context, app *App, services []string) error {
	p, err := app.plan(ctx, runOptions{Services: services})
	if err != nil {
		return err
	}

	stream := newLogStream(false, true, false)
	outcomes, err := p.runPhased(ctx, phasedOptions{}, func(ctx context.Context, r projectRun, ph phase) error {
		err := o.run(ctx, r, ph, stream.Consumer(r.Project.Name, replicasOf(r.Project)))
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	return runner.Err(outcomes)
}

func (o watchOptions) run(ctx context.Context, r projectRun, ph phase, consumer api.LogConsumer) error {
	project, err := r.Project.WithServicesEnvironmentResolved(true)
	if err != nil {
		return err
	}
	if err := applyPlatforms(project, true); err != nil {
		return err
	}
	build, err := buildOptions{quiet: o.quiet}.apiOptions(nil)
	if err != nil {
		return err
	}

	if !o.noUp {
		if err := o.up(ctx, r, project, build); err != nil {
			return err
		}
	}
	ph.Ready()
	return r.Compose.Watch(ctx, project, api.WatchOptions{
		Build:    &build,
		LogTo:    consumer,
		Prune:    o.prune,
		Services: r.Services,
	})
}

// up builds and starts the services before watching them.
func (o watchOptions) up(ctx context.Context, r projectRun, project *types.Project, build api.BuildOptions) error {
	for name, service := range project.Services {
		if service.Build != nil && service.Develop != nil {
			service.PullPolicy = types.PullPolicyBuild
			project.Services[name] = service
		}
	}
	// Build output would tear the live view; it is only shown on failure.
	upBuild := build
	return captureBuildLog(&upBuild, func() error {
		return r.Compose.Up(ctx, project, api.UpOptions{
			Create: api.CreateOptions{
				Build:                &upBuild,
				Services:             r.Services,
				Recreate:             api.RecreateDiverged,
				RecreateDependencies: api.RecreateNever,
				Inherit:              true,
				QuietPull:            o.quiet,
			},
			Start: api.StartOptions{Project: project, Services: r.Services},
		})
	})
}

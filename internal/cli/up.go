package cli

import (
	"context"
	"errors"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/runner"
)

func init() { Register(newUpCommand) }

type upOptions struct {
	create             createOptions
	detach             bool
	noStart            bool
	noDeps             bool
	cascadeStop        bool
	cascadeFail        bool
	exitCodeFrom       string
	noColor            bool
	noPrefix           bool
	attachDependencies bool
	attach             []string
	noAttach           []string
	timestamp          bool
	wait               bool
	waitTimeout        int
	watch              bool
	navigationMenu     bool
}

func newUpCommand(app *App) *cobra.Command {
	var opts upOptions
	cmd := &cobra.Command{
		Use:   "up [OPTIONS] [SERVICE...]",
		Short: "Create and start containers",
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			opts.create.pullChanged = flags.Changed("pull")
			opts.create.timeChanged = flags.Changed("timeout")
			if !flags.Changed("remove-orphans") {
				opts.create.removeOrphans = envBool(envRemoveOrphans)
			}
			return opts.validate()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !opts.noStart && !opts.detach {
				return errors.New("attached up is not supported yet; use -d to run containers in the background")
			}
			runOpts := opts.create.runOptions(args)
			return app.runEach(cmd.Context(), runOpts, func(ctx context.Context, r projectRun) error {
				return opts.run(ctx, app, r)
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&opts.detach, "detach", "d", false, "Detached mode: Run containers in the background")
	addCreateFlags(flags, &opts.create, `Pull image before running ("always"|"missing"|"never")`)
	flags.BoolVar(&opts.noColor, "no-color", false, "Produce monochrome output")
	flags.BoolVar(&opts.noPrefix, "no-log-prefix", false, "Don't print prefix in logs")
	flags.BoolVar(&opts.noStart, "no-start", false, "Don't start the services after creating them")
	flags.BoolVar(&opts.cascadeStop, "abort-on-container-exit", false, "Stops all containers if any container was stopped. Incompatible with -d")
	flags.BoolVar(&opts.cascadeFail, "abort-on-container-failure", false, "Stops all containers if any container exited with failure. Incompatible with -d")
	flags.StringVar(&opts.exitCodeFrom, "exit-code-from", "", "Return the exit code of the selected service container. Implies --abort-on-container-exit")
	flags.IntVarP(&opts.create.timeout, "timeout", "t", 0, "Use this timeout in seconds for container shutdown when attached or when containers are already running")
	flags.BoolVar(&opts.timestamp, "timestamps", false, "Show timestamps")
	flags.BoolVar(&opts.noDeps, "no-deps", false, "Don't start linked services")
	flags.BoolVar(&opts.create.recreateDeps, "always-recreate-deps", false, "Recreate dependent containers. Incompatible with --no-recreate.")
	flags.BoolVarP(&opts.create.noInherit, "renew-anon-volumes", "V", false, "Recreate anonymous volumes instead of retrieving data from the previous containers")
	flags.StringArrayVar(&opts.attach, "attach", []string{}, "Restrict attaching to the specified services. Incompatible with --attach-dependencies.")
	flags.StringArrayVar(&opts.noAttach, "no-attach", []string{}, "Do not attach (stream logs) to the specified services")
	flags.BoolVar(&opts.attachDependencies, "attach-dependencies", false, "Automatically attach to log output of dependent services")
	flags.BoolVar(&opts.wait, "wait", false, "Wait for services to be running|healthy. Implies detached mode.")
	flags.IntVar(&opts.waitTimeout, "wait-timeout", 0, "Maximum duration in seconds to wait for the project to be running|healthy")
	flags.BoolVarP(&opts.watch, "watch", "w", false, "Watch source code and rebuild/refresh containers when files are updated.")
	flags.BoolVar(&opts.navigationMenu, "menu", false, "Enable interactive shortcuts when running attached. Incompatible with --detach. Can also be enable/disable by setting COMPOSE_MENU environment var.")
	return cmd
}

// validate rejects incompatible flags, and applies the implications between them.
func (o *upOptions) validate() error {
	if o.waitTimeout < 0 {
		return errors.New("--wait-timeout must be a non-negative integer")
	}
	if o.exitCodeFrom != "" && !o.cascadeFail {
		o.cascadeStop = true
	}
	if o.cascadeStop && o.cascadeFail {
		return errors.New("--abort-on-container-failure cannot be combined with --abort-on-container-exit")
	}
	if o.wait {
		if o.attachDependencies || o.cascadeStop || len(o.attach) > 0 {
			return errors.New("--wait cannot be combined with --abort-on-container-exit, --attach or --attach-dependencies")
		}
		o.detach = true
	}
	if o.create.build && o.create.noBuild {
		return errors.New("--build and --no-build are incompatible")
	}
	if o.detach && (o.attachDependencies || o.cascadeStop || o.cascadeFail || len(o.attach) > 0 || o.watch) {
		if o.wait {
			return errors.New("--wait cannot be combined with --abort-on-container-exit, --abort-on-container-failure, --attach, --attach-dependencies or --watch")
		}
		return errors.New("--detach cannot be combined with --abort-on-container-exit, --abort-on-container-failure, --attach, --attach-dependencies or --watch")
	}
	switch {
	case o.create.noInherit && o.create.noRecreate:
		return errors.New("--no-recreate and --renew-anon-volumes are incompatible")
	case o.create.forceRecreate && o.create.noRecreate:
		return errors.New("--force-recreate and --no-recreate are incompatible")
	case o.create.recreateDeps && o.create.noRecreate:
		return errors.New("--always-recreate-deps and --no-recreate are incompatible")
	case o.create.noBuild && o.watch:
		return errors.New("--no-build and --watch are incompatible")
	}
	return o.create.validate()
}

func (o upOptions) run(ctx context.Context, app *App, r projectRun) error {
	project := r.Project
	if !app.BackendOptions().AllResources && len(project.Services) == 0 {
		return runner.Skip("no service selected")
	}
	if err := rejectScheduledJobs(project); err != nil {
		return err
	}
	if err := o.create.apply(project); err != nil {
		return err
	}
	if o.noDeps {
		var err error
		if project, err = project.WithSelectedServices(r.Services, types.IgnoreDependencies); err != nil {
			return err
		}
	}
	if o.exitCodeFrom != "" {
		if _, err := project.GetService(o.exitCodeFrom); err != nil {
			return err
		}
	}

	build := o.create.buildOptions(project.ServiceNames(), !o.noDeps)
	create := o.create.apiOptions(project, r.Services, build)
	if o.noStart {
		return r.Compose.Create(ctx, project, create)
	}
	return r.Compose.Up(ctx, project, api.UpOptions{
		Create: create,
		Start: api.StartOptions{
			Project:     project,
			Wait:        o.wait,
			WaitTimeout: waitTimeout(o.waitTimeout),
			Services:    r.Services,
		},
	})
}

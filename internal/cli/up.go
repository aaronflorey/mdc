package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/discover"
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
	quietBuild         bool
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
			runOpts := opts.create.runOptions(args)
			if !opts.noStart && !opts.detach {
				return opts.runAttached(cmd.Context(), app, runOpts)
			}
			return app.runEach(cmd.Context(), runOpts, func(ctx context.Context, r projectRun) error {
				return opts.run(ctx, app, r, nil)
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
	flags.BoolVar(&opts.quietBuild, "quiet-build", false, "Suppress the build output")
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
	if len(o.attach) > 0 && o.attachDependencies {
		return errors.New("cannot combine --attach and --attach-dependencies")
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

// logSession is how an attached up streams the logs of one project.
type logSession struct {
	consumer api.LogConsumer
	phase    phase
}

func (o upOptions) run(ctx context.Context, app *App, r projectRun, session *logSession) error {
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

	build := o.create.buildOptions(project.ServiceNames(), !o.noDeps)
	if build != nil {
		build.Quiet = o.quietBuild
	}
	create := o.create.apiOptions(project, r.Services, build)
	start := api.StartOptions{
		Project:     project,
		Wait:        o.wait,
		WaitTimeout: waitTimeout(o.waitTimeout),
		Services:    r.Services,
	}
	return captureBuildLog(create.Build, func() error {
		switch {
		case o.noStart:
			return r.Compose.Create(ctx, project, create)
		case session != nil:
			o.attachTo(&start, project, r.Services, session)
			return o.upAttached(ctx, r, project, create, start, session.phase)
		}
		return r.Compose.Up(ctx, project, api.UpOptions{Create: create, Start: start})
	})
}

// attachTo makes start stream the logs of the services this project attaches.
func (o upOptions) attachTo(start *api.StartOptions, project *types.Project, services []string, session *logSession) {
	start.Attach = session.consumer
	start.AttachTo = o.attachedServices(project, services)
	start.OnExit = o.onExit()
	start.Watch = o.watch
	if _, err := project.GetService(o.exitCodeFrom); err == nil {
		start.ExitCodeFrom = o.exitCodeFrom
	}
}

// upAttached runs up in the foreground. Compose handles interrupts itself
// once the session starts, so it gets a context that only follows the
// interrupt of the progress phase.
func (o upOptions) upAttached(ctx context.Context, r projectRun, project *types.Project, create api.CreateOptions, start api.StartOptions, ph phase) error {
	upCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		if !ph.Streaming() {
			cancel()
		}
	})
	defer stop()

	if len(start.AttachTo) == 0 {
		// Nothing to follow in this project: start it in the background.
		start.Attach = nil
	}
	return r.Compose.Up(upCtx, project, api.UpOptions{Create: create, Start: start})
}

func (o upOptions) onExit() api.Cascade {
	switch {
	case o.cascadeStop:
		return api.CascadeStop
	case o.cascadeFail:
		return api.CascadeFail
	}
	return api.CascadeIgnore
}

// attachedServices returns the services of the project whose logs up follows:
// the ones named with --attach, or else the services being launched (and their
// dependencies with --attach-dependencies) that have not opted out in the
// Compose file, minus the ones excluded with --no-attach.
func (o upOptions) attachedServices(project *types.Project, services []string) []string {
	attach := map[string]bool{}
	if len(o.attach) > 0 {
		for _, name := range o.attach {
			if _, ok := project.Services[name]; ok {
				attach[name] = true
			}
		}
	} else {
		dependencies := types.IgnoreDependencies
		if o.attachDependencies {
			dependencies = types.IncludeDependencies
		}
		_ = project.ForEachService(services, func(name string, s *types.ServiceConfig) error {
			if s.Attach == nil || *s.Attach {
				attach[name] = true
			}
			return nil
		}, dependencies)
	}
	for _, name := range o.noAttach {
		delete(attach, name)
	}
	return slices.Sorted(maps.Keys(attach))
}

// runAttached creates and starts every project under the live view, then
// follows the logs of all of them in one stream until they stop.
func (o upOptions) runAttached(ctx context.Context, app *App, runOpts runOptions) error {
	p, err := app.plan(ctx, runOpts)
	if err != nil {
		return err
	}
	if err := o.checkNames(p); err != nil {
		return err
	}
	stream := newLogStream(!o.noColor, !o.noPrefix, o.timestamp)
	outcomes, err := p.runPhased(ctx, phasedOptions{
		ReadyOnUp: true,
		Late: func(t discover.Target, r api.Resource) {
			stream.Notice(t.ProjectName, r.Text)
		},
	}, func(ctx context.Context, r projectRun, ph phase) error {
		session := &logSession{consumer: stream.Consumer(r.Project.Name, replicasOf(r.Project)), phase: ph}
		return o.run(ctx, app, r, session)
	})
	if err != nil {
		return err
	}
	for _, out := range outcomes {
		if code, ok := exitCodeOf(out.Err); ok && code != 0 {
			return ExitError{Code: code}
		}
	}
	return runner.Err(outcomes)
}

// checkNames rejects --attach and --exit-code-from services no project defines.
func (o upOptions) checkNames(p *plan) error {
	defined := map[string]bool{}
	for _, pp := range p.projects {
		if pp.project == nil {
			continue
		}
		for name := range pp.project.Services {
			defined[name] = true
		}
	}
	var unknown []string
	for _, name := range o.attach {
		if !defined[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("cannot attach to services not included in up: %s", strings.Join(unknown, ", "))
	}
	if o.exitCodeFrom != "" && !defined[o.exitCodeFrom] {
		return fmt.Errorf("no such service: %s", o.exitCodeFrom)
	}
	return nil
}

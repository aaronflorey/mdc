package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"

	"github.com/aaronflorey/mdc/internal/backend"
	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
	"github.com/aaronflorey/mdc/internal/ui"
)

// projectRun is what a command receives for one project.
type projectRun struct {
	Target discover.Target
	// Project is narrowed to Services (and their dependencies) when services
	// were requested.
	Project *types.Project
	// Services are the requested names this project defines; empty means the
	// whole project.
	Services []string
	Compose  api.Compose
}

// runOptions tune how projects are loaded and how compose is configured.
type runOptions struct {
	// Services are the requested names; empty selects whole projects.
	Services []string
	// WholeProject keeps every service in the project and only enables the
	// requested ones (for commands that act on dependents too).
	WholeProject bool
	// Compose options are applied last to each project's compose service.
	Compose []compose.Option
}

// plan holds the projects of one command run, loaded and matched against the
// requested services.
type plan struct {
	app      *App
	backend  *backend.Backend
	opts     runOptions
	targets  []discover.Target
	projects []plannedProject
}

type plannedProject struct {
	project  *types.Project
	services []string
	skip     bool
	err      error
}

// runEach discovers and loads the projects, then runs fn for every project
// that defines the requested services under the live view. Projects without
// them are skipped; it returns an error when any project failed.
func (a *App) runEach(ctx context.Context, opts runOptions, fn func(ctx context.Context, r projectRun) error) error {
	p, err := a.plan(ctx, opts)
	if err != nil {
		return err
	}
	return p.run(ctx, fn)
}

func (a *App) plan(ctx context.Context, opts runOptions) (*plan, error) {
	found, err := a.Discover()
	if err != nil {
		return nil, err
	}
	be, err := backend.New(a.BackendOptions())
	if err != nil {
		return nil, err
	}
	p := &plan{app: a, backend: be, opts: opts, targets: found.Targets, projects: make([]plannedProject, len(found.Targets))}

	var loaded []*types.Project
	var loadedIdx []int
	for i, t := range p.targets {
		project, err := be.LoadProject(ctx, t)
		if err != nil {
			p.projects[i].err = err
			continue
		}
		p.projects[i].project = project
		loaded = append(loaded, project)
		loadedIdx = append(loadedIdx, i)
	}

	res, err := backend.ResolveServices(opts.Services, loaded)
	if errors.Is(err, backend.ErrNoMatchingServices) {
		return nil, fmt.Errorf("%w: %s", err, strings.Join(opts.Services, ", "))
	}
	if err != nil {
		return nil, err
	}
	warnMissing(res.Missing)

	for n, sel := range res.Selections {
		pp := &p.projects[loadedIdx[n]]
		pp.services, pp.skip = sel.Services, sel.Skip
		if pp.skip || len(pp.services) == 0 {
			continue
		}
		if opts.WholeProject {
			pp.project, pp.err = pp.project.WithServicesEnabled(pp.services...)
		} else {
			pp.project, pp.err = be.LoadProject(ctx, p.targets[loadedIdx[n]], pp.services...)
		}
	}
	return p, nil
}

func warnMissing(names []string) {
	if len(names) == 0 {
		return
	}
	_, _ = lipgloss.Fprintln(os.Stderr, ui.Warn.Render("Warning: no project defines "+strings.Join(names, ", ")))
}

// run executes fn for each project under the live view.
func (p *plan) run(ctx context.Context, fn func(ctx context.Context, r projectRun) error) error {
	index := make(map[discover.Target]int, len(p.targets))
	for i, t := range p.targets {
		index[t] = i
	}
	outcomes, err := ui.RunLive(ctx, p.targets, func(ctx context.Context, live *ui.Live) []runner.Outcome {
		return runner.Run(ctx, p.targets, p.app.Jobs(), live.Track(func(ctx context.Context, t discover.Target) error {
			pp := p.projects[index[t]]
			switch {
			case pp.err != nil:
				return pp.err
			case pp.skip:
				return runner.Skip("no matching services")
			}
			svc, err := p.backend.Service(live.Processor(t), live.Prompt(t), p.opts.Compose...)
			if err != nil {
				return err
			}
			return fn(ctx, projectRun{Target: t, Project: pp.project, Services: pp.services, Compose: svc})
		}))
	})
	if err != nil {
		return err
	}
	return runner.Err(outcomes)
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/compose-spec/compose-go/v2/types"
	dockercli "github.com/docker/cli/cli"
	"github.com/docker/compose/v5/pkg/api"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
	"github.com/aaronflorey/mdc/internal/ui"
)

// newLogStream writes to the terminal. Colours follow the terminal and NO_COLOR.
func newLogStream(color, prefix, timestamps bool) *ui.LogStream {
	return ui.NewLogStream(ui.LogStreamOptions{
		Out:        ui.Terminal(os.Stdout),
		Err:        ui.Terminal(os.Stderr),
		Color:      color,
		Prefix:     prefix,
		Timestamps: timestamps,
	})
}

// replicasOf maps each service of the project to how many containers it runs.
func replicasOf(project *types.Project) map[string]int {
	replicas := make(map[string]int, len(project.Services))
	for name, service := range project.Services {
		replicas[name] = service.GetScale()
	}
	return replicas
}

// streamEach runs fn for every project that defines the requested services,
// printing what the projects stream as it arrives. Following commands run all
// projects at once so no stream waits for another to end; the others run at
// most --jobs at a time. An interrupt ends the streams without an error.
func (a *App) streamEach(ctx context.Context, opts runOptions, follow bool, fn func(ctx context.Context, r projectRun) error) error {
	p, err := a.plan(ctx, opts)
	if err != nil {
		return err
	}
	p.printSkipped()

	jobs := a.Jobs()
	if follow {
		jobs = 0
	}
	outcomes := runner.Run(ctx, p.targets, jobs, func(ctx context.Context, t discover.Target) error {
		return p.runProject(ctx, t, quietEvents{}, declinePrompt, func(ctx context.Context, r projectRun) error {
			err := fn(ctx, r)
			if ctx.Err() != nil && errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		})
	})
	return reportOutcomes(outcomes, false)
}

// printSkipped lists the projects that have nothing to do for the command.
func (p *plan) printSkipped() {
	for i, pp := range p.projects {
		if pp.skip && pp.err == nil {
			printLine(os.Stdout, ui.SkippedLine(p.targets[i], "no matching services"))
		}
	}
}

// phase is what a phased run gives each project.
type phase struct {
	// Ready ends the project's progress phase and blocks until every project
	// has done so and the live view has made way for streamed output.
	Ready func()
	// Streaming reports whether the live view has ended.
	Streaming func() bool
}

// phasedOptions tune runPhased.
type phasedOptions struct {
	// ReadyOnUp ends a project's progress phase when compose reports the
	// create phase of its up as done.
	ReadyOnUp bool
	// Late receives compose-level messages that arrive after the live view
	// ended.
	Late func(t discover.Target, r api.Resource)
}

// runPhased runs fn for every project at once under the live view, as the
// progress of the work that precedes streaming output. Once every project has
// called Ready (or returned) the view ends and fn continues into its
// streaming phase. Projects are not bounded by --jobs, since one waiting for
// the others would otherwise hold the rest back. Failures that happen after the
// view ended are printed here.
func (p *plan) runPhased(ctx context.Context, opts phasedOptions, fn func(ctx context.Context, r projectRun, ph phase) error) ([]runner.Outcome, error) {
	barrier := make(chan struct{})
	var streaming atomic.Bool
	waiting := atomic.Int32{}
	waiting.Store(int32(len(p.targets)))
	readied := make([]atomic.Bool, len(p.targets))

	outcomes, err := ui.RunLive(ctx, p.targets, func(ctx context.Context, live *ui.Live) []runner.Outcome {
		arrive := func() {
			if waiting.Add(-1) > 0 {
				return
			}
			_ = live.Stop()
			streaming.Store(true)
			close(barrier)
		}
		return runner.Run(ctx, p.targets, 0, live.Track(func(ctx context.Context, t discover.Target) error {
			var once sync.Once
			ready := func() {
				once.Do(func() {
					readied[p.index(t)].Store(true)
					live.Finish(runner.Outcome{Target: t})
					arrive()
				})
				<-barrier
			}
			defer once.Do(arrive)

			events := live.Processor(t)
			if opts.ReadyOnUp {
				events = readyOnUp{EventProcessor: events, ready: ready}
			}
			if opts.Late != nil {
				events = lateEvents{EventProcessor: events, streaming: &streaming, late: func(r api.Resource) { opts.Late(t, r) }}
			}
			return p.runProject(ctx, t, events, live.Prompt(t), func(ctx context.Context, r projectRun) error {
				return fn(ctx, r, phase{Ready: ready, Streaming: streaming.Load})
			})
		}))
	})
	if err != nil {
		return outcomes, err
	}
	for i, o := range outcomes {
		if _, isExit := exitCodeOf(o.Err); readied[i].Load() && o.Status == runner.Failed && !isExit {
			printLine(os.Stderr, ui.Failure(o.Target, o.Err))
		}
	}
	return outcomes, nil
}

// readyOnUp ends the progress phase when the create phase of up is done.
type readyOnUp struct {
	api.EventProcessor
	ready func()
}

func (r readyOnUp) Done(operation string, failed bool) {
	r.EventProcessor.Done(operation, failed)
	if operation == "up" && !failed {
		r.ready()
	}
}

// lateEvents forwards compose-level messages once the live view is gone.
type lateEvents struct {
	api.EventProcessor
	streaming *atomic.Bool
	late      func(api.Resource)
}

func (l lateEvents) On(events ...api.Resource) {
	if !l.streaming.Load() {
		l.EventProcessor.On(events...)
		return
	}
	for _, e := range events {
		if e.ID == api.ResourceCompose && e.Text != "" {
			l.late(e)
		}
	}
}

// ExitError asks the process to exit with a specific code, such as the exit
// code of a container, without printing anything further.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode extracts the process exit code requested by err.
func ExitCode(err error) (int, bool) {
	var exit ExitError
	if errors.As(err, &exit) {
		return exit.Code, true
	}
	return 0, false
}

// exitCodeOf extracts the exit status compose reports for a container.
func exitCodeOf(err error) (int, bool) {
	var status dockercli.StatusError
	if errors.As(err, &status) {
		return status.StatusCode, true
	}
	return 0, false
}

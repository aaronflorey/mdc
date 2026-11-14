package ui

import (
	"context"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
)

// Live is the handle given to the work running under RunLive. It is safe for
// concurrent use.
type Live struct {
	program *tea.Program
	index   map[discover.Target]int

	// promptMu serialises questions so only one is on screen at a time.
	promptMu sync.Mutex
	ctx      context.Context
}

// RunLive shows a live view of targets while fn runs, then prints the final
// summary (including every failure's error text) after the view exits. Ctrl-C
// cancels the context passed to fn. It returns what fn returned.
//
//	outcomes, err := ui.RunLive(ctx, targets, func(ctx context.Context, live *ui.Live) []runner.Outcome {
//		return runner.Run(ctx, targets, jobs, live.Track(func(ctx context.Context, t discover.Target) error {
//			svc, err := be.Service(live.Processor(t), live.Prompt(t))
//			if err != nil {
//				return err
//			}
//			return svc.Start(ctx, t.ProjectName, api.StartOptions{})
//		}))
//	})
func RunLive(ctx context.Context, targets []discover.Target, fn func(ctx context.Context, live *Live) []runner.Outcome) ([]runner.Outcome, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := newModel(targets, cancel)
	program := tea.NewProgram(m, tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		// Route SIGINT through cancellation so running work can report.
		if _, ok := msg.(tea.InterruptMsg); ok {
			return cancelMsg{}
		}
		return msg
	}))

	live := &Live{program: program, index: make(map[discover.Target]int, len(targets)), ctx: ctx}
	for i, t := range targets {
		live.index[t] = i
	}

	type result struct {
		model tea.Model
		err   error
	}
	ran := make(chan result, 1)
	go func() {
		final, err := program.Run()
		ran <- result{final, err}
	}()

	outcomes := fn(ctx, live)
	for _, o := range outcomes {
		live.Finish(o)
	}
	program.Send(doneMsg{})
	res := <-ran

	if res.model != nil {
		_, _ = lipgloss.Fprintln(os.Stdout, res.model.(*model).summary())
	}
	return outcomes, res.err
}

// Processor returns the event processor to give backend.Service for t.
func (l *Live) Processor(t discover.Target) api.EventProcessor {
	return processor{live: l, idx: l.index[t]}
}

// Prompt returns the compose prompt to give backend.Service for t. Questions
// from all targets are asked one at a time; cancellation declines them.
func (l *Live) Prompt(t discover.Target) compose.Prompt {
	idx := l.index[t]
	return func(message string, defaultValue bool) (bool, error) {
		l.promptMu.Lock()
		defer l.promptMu.Unlock()

		reply := make(chan bool, 1)
		l.program.Send(promptMsg{idx: idx, message: message, def: defaultValue, reply: reply})
		select {
		case yes := <-reply:
			return yes, nil
		case <-l.ctx.Done():
			return false, l.ctx.Err()
		}
	}
}

// Finish marks a target done with its outcome. Only the first call per target
// counts.
func (l *Live) Finish(o runner.Outcome) {
	l.program.Send(finishMsg{idx: l.index[o.Target], outcome: o})
}

// Track wraps fn so each target is marked done in the view as soon as it
// returns, with the outcome runner.Run will record for it.
func (l *Live) Track(fn runner.Func) runner.Func {
	return func(ctx context.Context, t discover.Target) error {
		o := runner.Run(ctx, []discover.Target{t}, 1, fn)[0]
		l.Finish(o)
		switch o.Status {
		case runner.OK:
			if o.Detail != "" {
				return runner.Note(o.Detail)
			}
		case runner.Skipped:
			return runner.Skip(o.Reason)
		case runner.Failed:
			return o.Err
		}
		return nil
	}
}

type processor struct {
	live *Live
	idx  int
}

func (p processor) Start(context.Context, string) {}
func (p processor) Done(string, bool)             {}

func (p processor) On(events ...api.Resource) {
	p.live.program.Send(eventsMsg{idx: p.idx, events: events})
}

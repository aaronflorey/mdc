// Package runner runs an operation across targets with bounded concurrency.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aaronflorey/mdc/internal/discover"
)

// Status is the result category of one target.
type Status int

const (
	OK Status = iota
	Skipped
	Failed
)

// Outcome is the result of running the operation for one target.
type Outcome struct {
	Target discover.Target
	Status Status
	// Reason explains a skipped target.
	Reason string
	// Detail is an optional note on a successful target.
	Detail string
	// Err is set for failed targets.
	Err error
}

// Func is the operation run for each target. Return Skip(reason) when the
// target has nothing to do.
type Func func(ctx context.Context, t discover.Target) error

type skipError struct{ reason string }

func (e *skipError) Error() string { return e.reason }

// Skip returns an error that marks the target as skipped rather than failed.
func Skip(reason string) error { return &skipError{reason: reason} }

type noteError struct{ detail string }

func (e *noteError) Error() string { return e.detail }

// Note returns an error that marks the target as successful and records detail
// on its outcome.
func Note(detail string) error { return &noteError{detail: detail} }

// Run calls fn for every target, at most jobs at a time (jobs <= 0 means all
// at once), and returns one Outcome per target in input order. Targets not
// started before ctx is cancelled fail with the context error.
func Run(ctx context.Context, targets []discover.Target, jobs int, fn Func) []Outcome {
	outcomes := make([]Outcome, len(targets))

	limit := jobs
	if limit <= 0 || limit > len(targets) {
		limit = len(targets)
	}
	slots := make(chan struct{}, limit)

	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes[i] = Outcome{Target: t}

			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				outcomes[i].Status, outcomes[i].Err = Failed, ctx.Err()
				return
			}

			var skip *skipError
			var note *noteError
			switch err := fn(ctx, t); {
			case err == nil:
			case errors.As(err, &note):
				outcomes[i].Detail = note.detail
			case errors.As(err, &skip):
				outcomes[i].Status, outcomes[i].Reason = Skipped, skip.reason
			default:
				outcomes[i].Status, outcomes[i].Err = Failed, err
			}
		}()
	}
	wg.Wait()

	return outcomes
}

// Err returns an error when any target failed, so the process can exit non-zero.
func Err(outcomes []Outcome) error {
	failed := 0
	for _, o := range outcomes {
		if o.Status == Failed {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d targets failed", failed, len(outcomes))
}

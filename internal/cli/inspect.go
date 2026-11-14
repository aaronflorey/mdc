package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/docker/compose/v5/pkg/api"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
	"github.com/aaronflorey/mdc/internal/ui"
)

// snapshot is what one project reported to a read-only command.
type snapshot[T any] struct {
	Target  discover.Target
	Outcome runner.Outcome
	Value   T
}

// quietEvents discards compose progress: inspection prints results, not progress.
type quietEvents struct{}

func (quietEvents) Start(context.Context, string) {}
func (quietEvents) Done(string, bool)             {}
func (quietEvents) On(...api.Resource)            {}

// collect runs fn for every project concurrently (bounded by --jobs) without
// the live view, so the caller can render everything once.
func collect[T any](ctx context.Context, a *App, opts runOptions, fn func(ctx context.Context, r projectRun) (T, error)) ([]snapshot[T], error) {
	p, err := a.plan(ctx, opts)
	if err != nil {
		return nil, err
	}
	values := make([]T, len(p.targets))
	outcomes := runner.Run(ctx, p.targets, a.Jobs(), func(ctx context.Context, t discover.Target) error {
		decline := func(_ string, def bool) (bool, error) { return def, nil }
		return p.runProject(ctx, t, quietEvents{}, decline, func(ctx context.Context, r projectRun) (err error) {
			values[p.index(t)], err = fn(ctx, r)
			return err
		})
	})
	snaps := make([]snapshot[T], len(outcomes))
	for i, o := range outcomes {
		snaps[i] = snapshot[T]{Target: o.Target, Outcome: o, Value: values[i]}
	}
	return snaps, nil
}

// succeeded returns the snapshots of projects that produced a result.
func succeeded[T any](snaps []snapshot[T]) []snapshot[T] {
	return slices.DeleteFunc(slices.Clone(snaps), func(s snapshot[T]) bool { return s.Outcome.Status != runner.OK })
}

// outcomesOf extracts the outcomes of snapshots.
func outcomesOf[T any](snaps []snapshot[T]) []runner.Outcome {
	out := make([]runner.Outcome, len(snaps))
	for i, s := range snaps {
		out[i] = s.Outcome
	}
	return out
}

// reportOutcomes prints skipped projects (when showSkipped) and then each
// failure, and returns the error that makes the command exit non-zero.
func reportOutcomes(outcomes []runner.Outcome, showSkipped bool) error {
	if showSkipped {
		for _, o := range outcomes {
			if o.Status == runner.Skipped {
				printLine(os.Stdout, ui.SkippedLine(o.Target, o.Reason))
			}
		}
	}
	for _, o := range outcomes {
		if o.Status == runner.Failed {
			printLine(os.Stderr, ui.Failure(o.Target, o.Err))
		}
	}
	return runner.Err(outcomes)
}

func printLine(w *os.File, s string) { _, _ = lipgloss.Fprintln(w, s) }

// outputFormat validates --format against the values a command supports.
func outputFormat(format string, allowed ...string) error {
	if slices.Contains(allowed, format) {
		return nil
	}
	return fmt.Errorf("unsupported format %q: use %s", format, strings.Join(allowed, " or "))
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// projectItem is one entry of a merged listing.
type projectItem[T any] struct {
	Project string
	Item    T
}

// merge flattens per-project listings into one, ordered by project and then by key.
func merge[T any](snaps []snapshot[[]T], key func(T) string) []projectItem[T] {
	var merged []projectItem[T]
	for _, s := range snaps {
		for _, item := range s.Value {
			merged = append(merged, projectItem[T]{Project: s.Target.ProjectName, Item: item})
		}
	}
	slices.SortStableFunc(merged, func(a, b projectItem[T]) int {
		if c := strings.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return strings.Compare(key(a.Item), key(b.Item))
	})
	return merged
}

func projectCell(project string) string { return ui.NameStyle(project).Render(project) }

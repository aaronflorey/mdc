package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/docker/cli/opts"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/backend"
	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newLsCommand) }

const notCreated = "not created"

func newLsCommand(app *App) *cobra.Command {
	var format string
	var quiet, all bool
	filter := opts.NewFilterOpt()
	cmd := &cobra.Command{
		Use:   "ls [OPTIONS]",
		Short: "List discovered compose projects",
		Args:  cobra.NoArgs,
		PreRunE: func(*cobra.Command, []string) error {
			return outputFormat(format, "table", "json")
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLs(cmd.Context(), app, lsOptions{format: format, quiet: quiet, all: all, filters: filter.Value()})
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&format, "format", "table", "Format the output. Values: [table | json]")
	flags.BoolVarP(&quiet, "quiet", "q", false, "Only display project names")
	flags.Var(&filter, "filter", "Filter output based on conditions provided")
	flags.BoolVarP(&all, "all", "a", false, "Show all stopped Compose projects")
	return cmd
}

type lsOptions struct {
	format  string
	quiet   bool
	all     bool
	filters client.Filters
}

type lsEntry struct {
	Target discover.Target
	Status string
}

func runLs(ctx context.Context, app *App, o lsOptions) error {
	for name := range o.filters {
		if name != "name" {
			return errors.New("invalid filter '" + name + "'")
		}
	}
	found, err := app.Discover()
	if err != nil {
		return err
	}
	be, err := backend.New(app.BackendOptions())
	if err != nil {
		return err
	}
	svc, err := be.Service(quietEvents{}, func(_ string, def bool) (bool, error) { return def, nil })
	if err != nil {
		return err
	}
	stacks, err := svc.List(ctx, api.ListOptions{All: true})
	if err != nil {
		return err
	}
	entries, err := lsEntries(found.Targets, stacks, o)
	if err != nil {
		return err
	}

	switch {
	case o.quiet:
		for _, e := range entries {
			printLine(os.Stdout, e.Target.ProjectName)
		}
	case o.format == "json":
		type project struct {
			Name        string
			Status      string
			ConfigFiles string
		}
		list := make([]project, len(entries))
		for i, e := range entries {
			list[i] = project{Name: e.Target.ProjectName, Status: e.Status, ConfigFiles: e.Target.File}
		}
		return printJSON(list)
	case len(entries) > 0:
		cols := []ui.Column{{Title: "NAME"}, {Title: "STATUS"}, {Title: "PATH", Drop: 1}}
		cells := make([][]string, len(entries))
		for i, e := range entries {
			cells[i] = []string{projectCell(e.Target.ProjectName), styledStatus(e.Status), e.Target.Label}
		}
		printLine(os.Stdout, ui.RenderTable(cols, cells, ui.TerminalWidth()))
	}
	return nil
}

// lsEntries pairs each discovered project with its status. Projects without
// containers are "not created". Unless all is set, only projects with a
// running container are kept.
func lsEntries(targets []discover.Target, stacks []api.Stack, o lsOptions) ([]lsEntry, error) {
	status := make(map[string]string, len(stacks))
	for _, s := range stacks {
		status[s.Name] = s.Status
	}
	var entries []lsEntry
	for _, t := range targets {
		s, ok := status[t.ProjectName]
		if !ok {
			s = notCreated
		}
		if !o.all && !hasRunning(s) {
			continue
		}
		match, err := matchesName(o.filters["name"], t.ProjectName)
		if err != nil {
			return nil, err
		}
		if match {
			entries = append(entries, lsEntry{Target: t, Status: s})
		}
	}
	return entries, nil
}

// matchesName reports whether name equals one of the patterns or matches it as
// a regular expression. No patterns match everything.
func matchesName(patterns map[string]bool, name string) (bool, error) {
	if len(patterns) == 0 || patterns[name] {
		return true, nil
	}
	for pattern := range patterns {
		if ok, err := regexp.MatchString(pattern, name); err == nil && ok {
			return true, nil
		}
	}
	return false, nil
}

// statusCount is one "state(count)" part of a compose stack status.
type statusCount struct {
	State string
	Count int
}

// parseStatus splits a stack status such as "running(2), exited(1)".
func parseStatus(status string) []statusCount {
	var parts []statusCount
	for _, part := range strings.Split(status, ", ") {
		state, rest, ok := strings.Cut(part, "(")
		count, err := strconv.Atoi(strings.TrimSuffix(rest, ")"))
		if !ok || err != nil {
			continue
		}
		parts = append(parts, statusCount{State: state, Count: count})
	}
	return parts
}

func hasRunning(status string) bool {
	for _, p := range parseStatus(status) {
		if p.State == "running" && p.Count > 0 {
			return true
		}
	}
	return false
}

func styledStatus(status string) string {
	parts := parseStatus(status)
	if len(parts) == 0 {
		return ui.Dim.Render(status)
	}
	styled := make([]string, len(parts))
	for i, p := range parts {
		styled[i] = ui.StateStyle(p.State, "").Render(fmt.Sprintf("%s(%d)", p.State, p.Count))
	}
	return strings.Join(styled, ", ")
}

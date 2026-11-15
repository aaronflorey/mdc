package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/aaronflorey/mdc/internal/backend"
	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newStatsCommand) }

const (
	statsRefreshInterval = 2 * time.Second
	// statsConcurrency bounds the samples collected at the same time: the
	// daemon needs about a second to sample a container.
	statsConcurrency = 32
)

type statsOptions struct {
	all         bool
	format      string
	noStream    bool
	noTrunc     bool
	allProjects bool
	aggregate   bool
}

func newStatsCommand(app *App) *cobra.Command {
	var opts statsOptions
	cmd := &cobra.Command{
		Use:   "stats [OPTIONS] [SERVICE]",
		Short: "Display a live stream of container(s) resource usage statistics",
		Args:  cobra.MaximumNArgs(1),
		PreRunE: func(_ *cobra.Command, args []string) error {
			return opts.validate(args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context(), app, args)
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&opts.all, "all", "a", false, "Show all containers (default shows just running)")
	flags.StringVar(&opts.format, "format", "", "Format output: 'table' (default) or 'json'")
	flags.BoolVar(&opts.noStream, "no-stream", false, "Disable streaming stats and only pull the first result")
	flags.BoolVar(&opts.noTrunc, "no-trunc", false, "Do not truncate output")
	flags.BoolVar(&opts.allProjects, "all-projects", false, "Show the containers of all projects instead of the discovered ones, with their project (only 'table' and 'json' formats)")
	flags.BoolVar(&opts.aggregate, "aggregate", false, "Show one line per project, summing the resource usage of its containers (only 'table' and 'json' formats)")
	return cmd
}

func (o statsOptions) validate(services []string) error {
	if err := outputFormat(o.format, "", "table", "json"); err != nil {
		return err
	}
	if len(services) > 0 && (o.allProjects || o.aggregate) {
		var flags []string
		if o.allProjects {
			flags = append(flags, "--all-projects")
		}
		if o.aggregate {
			flags = append(flags, "--aggregate")
		}
		return fmt.Errorf("%s report on whole projects and cannot be used with a SERVICE", strings.Join(flags, " and "))
	}
	return nil
}

// statsScope selects the containers to report on.
type statsScope struct {
	// projects are the projects to include; nil includes every compose project.
	projects []string
	// services restricts the projects to these services when set.
	services map[string][]string
}

func (o statsOptions) scope(ctx context.Context, app *App, services []string) (statsScope, error) {
	if o.allProjects {
		return statsScope{}, nil
	}
	if len(services) == 0 {
		found, err := app.Discover()
		if err != nil {
			return statsScope{}, err
		}
		scope := statsScope{projects: []string{}}
		for _, t := range found.Targets {
			scope.projects = append(scope.projects, t.ProjectName)
		}
		return scope, nil
	}
	p, err := app.plan(ctx, runOptions{Services: services})
	if err != nil {
		return statsScope{}, err
	}
	scope := statsScope{projects: []string{}, services: map[string][]string{}}
	for i, pp := range p.projects {
		if pp.skip || pp.err != nil {
			continue
		}
		name := p.targets[i].ProjectName
		scope.projects = append(scope.projects, name)
		scope.services[name] = pp.services
	}
	return scope, nil
}

func (o statsOptions) run(ctx context.Context, app *App, services []string) error {
	scope, err := o.scope(ctx, app, services)
	if err != nil {
		return err
	}
	be, err := backend.New(app.BackendOptions())
	if err != nil {
		return err
	}
	collector := statsCollector{client: be.Client(), scope: scope, all: o.all}

	switch {
	case o.format == "json":
		return o.streamJSON(ctx, collector)
	case o.noStream:
		rows, err := collector.snapshot(ctx)
		if err != nil {
			return err
		}
		width := ui.TerminalWidth()
		if o.noTrunc {
			width = math.MaxInt32
		}
		printLine(os.Stdout, ui.RenderStats(rows, o.aggregate, width))
		return nil
	}
	return ui.RunStats(ctx, o.aggregate, func(ctx context.Context, sink ui.StatsSink) error {
		return collector.stream(ctx, sink)
	})
}

// streamJSON prints one JSON object per line, for each refresh until
// interrupted, or once with --no-stream.
func (o statsOptions) streamJSON(ctx context.Context, c statsCollector) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	for {
		rows, err := c.snapshot(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if o.aggregate {
			for _, v := range ui.ProjectViews(rows) {
				if err := enc.Encode(v); err != nil {
					return err
				}
			}
		} else {
			for _, v := range ui.ContainerViews(rows) {
				if err := enc.Encode(v); err != nil {
					return err
				}
			}
		}
		if o.noStream {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(statsRefreshInterval):
		}
	}
}

// statsCollector lists and samples containers on the Docker engine.
type statsCollector struct {
	client client.ContainerAPIClient
	scope  statsScope
	all    bool
}

// containers lists the containers in scope.
func (c statsCollector) containers(ctx context.Context) ([]container.Summary, error) {
	list, err := c.client.ContainerList(ctx, client.ContainerListOptions{All: c.all, Filters: client.Filters{}.Add("label", api.ProjectLabel)})
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(list.Items, func(s container.Summary) bool { return !c.scope.includes(s) }), nil
}

func (s statsScope) includes(ctr container.Summary) bool {
	if s.projects == nil {
		return true
	}
	project := ctr.Labels[api.ProjectLabel]
	if !slices.Contains(s.projects, project) {
		return false
	}
	services, restricted := s.services[project]
	return !restricted || slices.Contains(services, ctr.Labels[api.ServiceLabel])
}

func listed(ctr container.Summary) ui.ContainerStats {
	return ui.ContainerStats{
		ID:      ctr.ID,
		Project: ctr.Labels[api.ProjectLabel],
		Name:    strings.TrimPrefix(firstOf(ctr.Names), "/"),
	}
}

func firstOf(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// snapshot samples every running container once.
func (c statsCollector) snapshot(ctx context.Context) ([]ui.ContainerStats, error) {
	list, err := c.containers(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]ui.ContainerStats, len(list))
	var eg errgroup.Group
	eg.SetLimit(statsConcurrency)
	for i, ctr := range list {
		rows[i] = listed(ctr)
		if ctr.State != container.StateRunning {
			continue
		}
		eg.Go(func() error {
			// A container that cannot be sampled shows no usage instead of
			// stopping the others.
			if limit, usage, err := sampleStats(ctx, c.client, ctr.ID); err == nil {
				rows[i].Sampled, rows[i].MemLimit, rows[i].Usage = true, limit, usage
			}
			return nil
		})
	}
	_ = eg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b ui.ContainerStats) int {
		return strings.Compare(a.Project+"/"+a.Name, b.Project+"/"+b.Name)
	})
	return rows, nil
}

// stream keeps sink up to date until ctx ends: each refresh lists the
// containers, then reports samples as they arrive.
func (c statsCollector) stream(ctx context.Context, sink ui.StatsSink) error {
	for {
		list, err := c.containers(ctx)
		if err != nil {
			return err
		}
		rows := make([]ui.ContainerStats, len(list))
		for i, ctr := range list {
			rows[i] = listed(ctr)
		}
		sink.List(rows)

		var eg errgroup.Group
		eg.SetLimit(statsConcurrency)
		for _, ctr := range list {
			if ctr.State != container.StateRunning {
				continue
			}
			eg.Go(func() error {
				if limit, usage, err := sampleStats(ctx, c.client, ctr.ID); err == nil {
					sink.Sample(ctr.ID, limit, usage)
				}
				return nil
			})
		}
		_ = eg.Wait()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(statsRefreshInterval):
		}
	}
}

// sampleStats takes one sample of a container; the daemon provides the
// previous sample so a single read is enough to compute CPU usage.
func sampleStats(ctx context.Context, apiClient client.ContainerAPIClient, id string) (memLimit float64, usage ui.Usage, err error) {
	res, err := apiClient.ContainerStats(ctx, id, client.ContainerStatsOptions{IncludePreviousSample: true})
	if err != nil {
		return 0, ui.Usage{}, err
	}
	defer res.Body.Close() //nolint:errcheck

	var v container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		return 0, ui.Usage{}, err
	}
	return float64(v.MemoryStats.Limit), containerUsage(v), nil
}

// containerUsage computes the same figures as `docker stats` for a Linux
// container, following docker/cli's cli/command/container/stats_helpers.go.
func containerUsage(v container.StatsResponse) ui.Usage {
	u := ui.Usage{
		Memory: float64(v.MemoryStats.Usage),
		PIDs:   v.PidsStats.Current,
	}

	// Page cache is not memory the container needs: cgroup v1 reports it as
	// total_inactive_file, cgroup v2 as inactive_file.
	for _, key := range []string{"total_inactive_file", "inactive_file"} {
		if cache, ok := v.MemoryStats.Stats[key]; ok && cache < v.MemoryStats.Usage {
			u.Memory = float64(v.MemoryStats.Usage - cache)
			break
		}
	}

	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	onlineCPUs := float64(v.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta > 0 && systemDelta > 0 {
		u.CPU = cpuDelta / systemDelta * onlineCPUs * 100
	}

	for _, n := range v.Networks {
		u.NetRx += float64(n.RxBytes)
		u.NetTx += float64(n.TxBytes)
	}
	for _, e := range v.BlkioStats.IoServiceBytesRecursive {
		switch {
		case strings.EqualFold(e.Op, "read"):
			u.BlockRead += float64(e.Value)
		case strings.EqualFold(e.Op, "write"):
			u.BlockWrite += float64(e.Value)
		}
	}
	return u
}

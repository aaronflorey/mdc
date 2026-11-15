package cli

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/docker/cli/cli/command/formatter"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/container"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newPsCommand) }

type psOptions struct {
	format   string
	all      bool
	quiet    bool
	services bool
	filter   string
	status   []string
	noTrunc  bool
	orphans  bool
}

func (o *psOptions) parseFilter() error {
	if o.filter == "" {
		return nil
	}
	key, val, ok := strings.Cut(o.filter, "=")
	if !ok {
		return errors.New("arguments to --filter should be in form KEY=VAL")
	}
	switch key {
	case "status":
		o.status = append(o.status, val)
		return nil
	case "source":
		return api.ErrNotImplemented
	default:
		return fmt.Errorf("unknown filter %s", key)
	}
}

func newPsCommand(app *App) *cobra.Command {
	var opts psOptions
	cmd := &cobra.Command{
		Use:   "ps [OPTIONS] [SERVICE...]",
		Short: "List containers",
		PreRunE: func(*cobra.Command, []string) error {
			if err := outputFormat(opts.format, "table", "json"); err != nil {
				return err
			}
			return opts.parseFilter()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPs(cmd.Context(), app, args, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.format, "format", "table", "Format the output. Values: [table | json]")
	flags.StringVar(&opts.filter, "filter", "", "Filter services by a property (supported filters: status)")
	flags.StringArrayVar(&opts.status, "status", []string{}, "Filter services by status. Values: [paused | restarting | removing | running | dead | created | exited]")
	flags.BoolVarP(&opts.quiet, "quiet", "q", false, "Only display IDs")
	flags.BoolVar(&opts.services, "services", false, "Display services")
	flags.BoolVar(&opts.orphans, "orphans", true, "Include orphaned services (not declared by project)")
	flags.BoolVarP(&opts.all, "all", "a", false, "Show all stopped containers (including those created by the run command)")
	flags.BoolVar(&opts.noTrunc, "no-trunc", false, "Don't truncate output")
	return cmd
}

func runPs(ctx context.Context, app *App, args []string, opts psOptions) error {
	snaps, err := collect(ctx, app, runOptions{Services: args}, func(ctx context.Context, r projectRun) ([]api.ContainerSummary, error) {
		services := r.Services
		if len(services) == 0 && !opts.orphans {
			// Only list services the project declares.
			services = r.Project.ServiceNames()
		}
		containers, err := r.Compose.Ps(ctx, r.Project.Name, api.PsOptions{
			Project:  r.Project,
			All:      opts.all || len(opts.status) != 0,
			Services: services,
		})
		if err != nil {
			return nil, err
		}
		if len(opts.status) != 0 {
			containers = slices.DeleteFunc(containers, func(c api.ContainerSummary) bool {
				return !slices.Contains(opts.status, string(c.State))
			})
		}
		return containers, nil
	})
	if err != nil {
		return err
	}
	rows := merge(succeeded(snaps), func(c api.ContainerSummary) string { return c.Service + "\x00" + c.Name })

	switch {
	case opts.quiet:
		for _, row := range rows {
			printLine(os.Stdout, row.Item.ID)
		}
	case opts.services:
		var seen []string
		for _, row := range rows {
			if !slices.Contains(seen, row.Item.Service) {
				seen = append(seen, row.Item.Service)
				printLine(os.Stdout, row.Item.Service)
			}
		}
	case opts.format == "json":
		list := make([]api.ContainerSummary, len(rows))
		for i, row := range rows {
			list[i] = row.Item
		}
		if err := printJSON(list); err != nil {
			return err
		}
	case len(rows) > 0:
		printLine(os.Stdout, psTable(rows, opts.noTrunc))
	}
	return reportOutcomes(outcomesOf(snaps), opts.format != "json" && !opts.quiet && !opts.services)
}

func psTable(rows []projectItem[api.ContainerSummary], noTrunc bool) string {
	cols := []ui.Column{
		{Title: "PROJECT"}, {Title: "SERVICE"}, {Title: "NAME", Drop: 3}, {Title: "IMAGE", Drop: 4},
		{Title: "COMMAND", Drop: 6}, {Title: "CREATED", Drop: 5}, {Title: "STATUS"}, {Title: "PORTS", Drop: 2},
	}
	cells := make([][]string, len(rows))
	for i, row := range rows {
		c := row.Item
		command := c.Command
		if !noTrunc && len(command) > 20 {
			command = command[:19] + "…"
		}
		status := ui.StateStyle(string(c.State), string(c.Health)).Render(c.Status)
		cells[i] = []string{
			projectCell(row.Project), c.Service, c.Name, c.Image, `"` + command + `"`,
			units.HumanDuration(time.Since(time.Unix(c.Created, 0))) + " ago", status, ports(c.Publishers),
		}
	}
	return ui.RenderTable(cols, cells, ui.TerminalWidth())
}

func ports(publishers api.PortPublishers) string {
	var summaries []container.PortSummary
	for _, p := range publishers {
		var ip netip.Addr
		if p.URL != "" {
			ip, _ = netip.ParseAddr(p.URL)
		}
		summaries = append(summaries, container.PortSummary{
			IP: ip, PrivatePort: uint16(p.TargetPort), PublicPort: uint16(p.PublishedPort), Type: p.Protocol,
		})
	}
	return formatter.DisplayablePorts(summaries)
}

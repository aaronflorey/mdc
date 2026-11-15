package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/moby/moby/api/types/container"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/runner"
)

func init() { Register(newRmCommand) }

func newRmCommand(app *App) *cobra.Command {
	var force, stop, volumes bool
	cmd := &cobra.Command{
		Use:   "rm [OPTIONS] [SERVICE...]",
		Short: "Removes stopped service containers",
		Long: `Removes stopped service containers

By default, anonymous volumes attached to containers will not be removed. You
can override this with -v. To list all volumes, use "docker volume ls".

Any data which is not in a volume will be lost.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p, err := app.plan(ctx, runOptions{Services: args})
			if err != nil {
				return err
			}
			if !force {
				confirmed, err := p.confirmRemoval(ctx, stop)
				if err != nil || !confirmed {
					return err
				}
			}
			return p.run(ctx, func(ctx context.Context, r projectRun) error {
				err := r.Compose.Remove(ctx, r.Project.Name, api.RemoveOptions{
					Services: r.Services,
					Force:    true,
					Volumes:  volumes,
					Project:  r.Project,
					Stop:     stop,
				})
				if errors.Is(err, api.ErrNoResources) {
					return runner.Note("no stopped containers")
				}
				return err
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&force, "force", "f", false, "Don't ask to confirm removal")
	flags.BoolVarP(&stop, "stop", "s", false, "Stop the containers, if required, before removing")
	flags.BoolVarP(&volumes, "volumes", "v", false, "Remove any anonymous volumes attached to containers")
	flags.BoolP("all", "a", false, "Deprecated - no effect")
	_ = flags.MarkHidden("all")
	return cmd
}

// confirmRemoval lists the containers that rm would remove across all
// projects and asks once. It reports false when there is nothing to remove or
// the answer is no.
func (p *plan) confirmRemoval(ctx context.Context, includeRunning bool) (bool, error) {
	var lines []string
	for i, pp := range p.projects {
		if pp.skip || pp.err != nil {
			continue
		}
		svc, err := p.backend.Service(nil, nil)
		if err != nil {
			return false, err
		}
		containers, err := svc.Ps(ctx, pp.project.Name, api.PsOptions{Project: pp.project, All: true, Services: pp.services})
		if err != nil {
			return false, fmt.Errorf("%s: %w", p.targets[i].ProjectName, err)
		}
		for _, c := range containers {
			if includeRunning || !isActive(c.State) {
				lines = append(lines, fmt.Sprintf("  %s  %s", p.targets[i].ProjectName, c.Name))
			}
		}
	}
	if len(lines) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "No stopped containers")
		return false, nil
	}
	_, _ = fmt.Fprintf(os.Stdout, "Going to remove:\n%s\nAre you sure? [y/N] ", strings.Join(lines, "\n"))
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && answer == "" {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

// isActive reports whether the container still counts as running to compose.
func isActive(state container.ContainerState) bool {
	switch state {
	case container.StateRunning, container.StatePaused, container.StateRestarting, container.StateRemoving:
		return true
	}
	return false
}

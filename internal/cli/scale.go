package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"
)

func init() { Register(newScaleCommand) }

func newScaleCommand(app *App) *cobra.Command {
	var noDeps bool
	cmd := &cobra.Command{
		Use:   "scale [SERVICE=REPLICAS...]",
		Short: "Scale services ",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			replicas, err := parseReplicas(args)
			if err != nil {
				return err
			}
			opts := runOptions{Services: slices.Sorted(maps.Keys(replicas))}
			return app.runEach(cmd.Context(), opts, func(ctx context.Context, r projectRun) error {
				project := r.Project
				if noDeps {
					var err error
					if project, err = project.WithSelectedServices(r.Services, types.IgnoreDependencies); err != nil {
						return err
					}
				}
				mine := make(map[string]int, len(r.Services))
				for _, name := range r.Services {
					mine[name] = replicas[name]
				}
				return r.Compose.Scale(ctx, project, api.ScaleOptions{Replicas: mine})
			})
		},
	}
	cmd.Flags().BoolVar(&noDeps, "no-deps", false, "Don't start linked services")
	return cmd
}

func parseReplicas(args []string) (map[string]int, error) {
	replicas := map[string]int{}
	for _, arg := range args {
		name, val, ok := strings.Cut(arg, "=")
		if !ok || name == "" || val == "" {
			return nil, fmt.Errorf("invalid scale specifier: %s", arg)
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid scale specifier: can't parse replica value as int: %v", arg)
		}
		replicas[name] = n
	}
	return replicas, nil
}

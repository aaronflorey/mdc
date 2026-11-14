package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/ui"
)

func init() {
	Register(newPullCommand)
	Register(newPushCommand)
}

type pullOptions struct {
	quiet              bool
	includeDeps        bool
	ignorePullFailures bool
	noBuildable        bool
	policy             string
}

func newPullCommand(app *App) *cobra.Command {
	var opts pullOptions
	var parallel, noParallel bool
	cmd := &cobra.Command{
		Use:   "pull [OPTIONS] [SERVICE...]",
		Short: "Pull service images",
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			for _, name := range []string{"no-parallel", "parallel"} {
				if cmd.Flags().Changed(name) {
					fmt.Fprintln(os.Stderr, ui.Err.Render("option '--"+name+"' is DEPRECATED and will be ignored."))
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				project, err := opts.apply(r.Project, r.Services)
				if err != nil {
					return err
				}
				return r.Compose.Pull(ctx, project, api.PullOptions{
					Quiet:           opts.quiet,
					IgnoreFailures:  opts.ignorePullFailures,
					IgnoreBuildable: opts.noBuildable,
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVarP(&opts.quiet, "quiet", "q", false, "Pull without printing progress information")
	flags.BoolVar(&opts.includeDeps, "include-deps", false, "Also pull services declared as dependencies")
	flags.BoolVar(&parallel, "parallel", true, "DEPRECATED pull multiple images in parallel")
	_ = flags.MarkHidden("parallel")
	flags.BoolVar(&noParallel, "no-parallel", true, "DEPRECATED disable parallel pulling")
	_ = flags.MarkHidden("no-parallel")
	flags.BoolVar(&opts.ignorePullFailures, "ignore-pull-failures", false, "Pull what it can and ignores images with pull failures")
	flags.BoolVar(&opts.noBuildable, "ignore-buildable", false, "Ignore images that can be built")
	flags.StringVar(&opts.policy, "policy", "", `Apply pull policy ("missing"|"always")`)
	return cmd
}

func (o pullOptions) apply(project *types.Project, services []string) (*types.Project, error) {
	if !o.includeDeps {
		var err error
		project, err = project.WithSelectedServices(services, types.IgnoreDependencies)
		if err != nil {
			return nil, err
		}
	}
	if o.policy != "" {
		for name, service := range project.Services {
			if service.Image == "" {
				continue
			}
			service.PullPolicy = o.policy
			project.Services[name] = service
		}
	}
	return project, nil
}

type pushOptions struct {
	includeDeps    bool
	ignoreFailures bool
	ignoreMissing  bool
	quiet          bool
}

func newPushCommand(app *App) *cobra.Command {
	var opts pushOptions
	cmd := &cobra.Command{
		Use:   "push [OPTIONS] [SERVICE...]",
		Short: "Push service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				project := r.Project
				if !opts.includeDeps {
					var err error
					if project, err = project.WithSelectedServices(r.Services, types.IgnoreDependencies); err != nil {
						return err
					}
				}
				return r.Compose.Push(ctx, project, api.PushOptions{
					IgnoreFailures: opts.ignoreFailures,
					IgnoreMissing:  opts.ignoreMissing,
					Quiet:          opts.quiet,
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.ignoreFailures, "ignore-push-failures", false, "Push what it can and ignores images with push failures")
	flags.BoolVar(&opts.ignoreMissing, "ignore-missing", false, "Skip images that are not available locally instead of failing to push them")
	flags.BoolVar(&opts.includeDeps, "include-deps", false, "Also push images of services declared as dependencies")
	flags.BoolVarP(&opts.quiet, "quiet", "q", false, "Push without printing progress information")
	_ = flags.MarkDeprecated("ignore-push-failures", "use --ignore-missing to skip images that are not available locally")
	cmd.MarkFlagsMutuallyExclusive("ignore-push-failures", "ignore-missing")
	return cmd
}

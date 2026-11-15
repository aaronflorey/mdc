package cli

import (
	"context"
	"fmt"

	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() { Register(newDownCommand) }

func newDownCommand(app *App) *cobra.Command {
	var (
		removeOrphans bool
		timeout       int
		volumes       bool
		images        string
	)
	cmd := &cobra.Command{
		Use:   "down [OPTIONS] [SERVICES]",
		Short: "Stop and remove containers, networks",
		PreRunE: func(*cobra.Command, []string) error {
			if !api.ImagePruneMode(images).Valid() {
				return fmt.Errorf("invalid value for --rmi: %q (legal values are %q, %q)", images, api.ImagePruneLocal, api.ImagePruneAll)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			timeoutChanged := cmd.Flags().Changed("timeout")
			return app.runEach(cmd.Context(), runOptions{Services: args}, func(ctx context.Context, r projectRun) error {
				return r.Compose.Down(ctx, r.Project.Name, api.DownOptions{
					RemoveOrphans: removeOrphans,
					Project:       r.Project,
					Timeout:       optionalTimeout(timeout, timeoutChanged),
					Images:        api.ImagePruneMode(images),
					Volumes:       volumes,
					Services:      r.Services,
				})
			})
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&removeOrphans, "remove-orphans", envBool(envRemoveOrphans), "Remove containers for services not defined in the Compose file")
	flags.IntVarP(&timeout, "timeout", "t", 0, "Specify a shutdown timeout in seconds")
	flags.BoolVarP(&volumes, "volumes", "v", false, `Remove named volumes declared in the "volumes" section of the Compose file and anonymous volumes attached to containers`)
	flags.StringVar(&images, "rmi", "", `Remove images used by services. "local" remove only images that don't have a custom tag ("local"|"all")`)
	flags.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "volume" {
			name = "volumes"
		}
		return pflag.NormalizedName(name)
	})
	return cmd
}

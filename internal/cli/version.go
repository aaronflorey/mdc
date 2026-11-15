package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/version"
)

const composeModule = "github.com/docker/compose/v5"

func init() { Register(newVersionCommand) }

func newVersionCommand(*App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the mdc and compose library versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "mdc %s (commit %s, built %s)\ncompose library %s\n",
				version.Version, version.Commit, version.Date, composeVersion())
			return err
		},
	}
}

func composeVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == composeModule {
			return dep.Version
		}
	}
	return "unknown"
}

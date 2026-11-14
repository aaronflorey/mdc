// Package cli defines the mdc command tree.
package cli

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Execute runs the root command and returns its error for the caller to report.
func Execute(ctx context.Context) error {
	return newRoot().ExecuteContext(ctx)
}

func newRoot() *cobra.Command {
	app := newApp()

	root := &cobra.Command{
		Use:           "mdc",
		Short:         "Run docker compose across multiple projects",
		Long:          "mdc discovers the compose projects below the current directory and runs compose commands across all of them.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := app.loadConfigFile(); err != nil {
				return err
			}
			return requireTerminal(cmd)
		},
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		_ = cmd.Usage()
		return err
	})
	if err := app.addGlobalFlags(root.PersistentFlags()); err != nil {
		// Binding only fails on a programming error in the flag definitions.
		panic(err)
	}

	for _, f := range factories {
		root.AddCommand(f(app))
	}
	return root
}

// requireTerminal rejects non-interactive use, except for commands that only print static text.
func requireTerminal(cmd *cobra.Command) error {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "version", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return nil
		}
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("mdc is interactive and needs a terminal; run it directly in a terminal rather than piping or redirecting its output")
	}
	return nil
}

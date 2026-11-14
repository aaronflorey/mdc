package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/aaronflorey/mdc/internal/backend"
	"github.com/aaronflorey/mdc/internal/discover"
)

const envPrefix = "MDC"

// App gives commands access to the global settings, resolved from flags,
// MDC_* environment variables and the optional config file.
type App struct {
	v *viper.Viper
}

func newApp() *App {
	v := viper.New()
	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	return &App{v: v}
}

// addGlobalFlags defines the root persistent flags and binds them to Viper.
func (a *App) addGlobalFlags(flags *pflag.FlagSet) error {
	flags.Int("depth", 1, "Directory depth to scan for compose projects")
	flags.Int("jobs", 0, "Maximum projects to run at once (0 = all)")
	flags.StringArray("profile", nil, "Specify a profile to enable")
	flags.StringArray("env-file", nil, "Specify an alternate environment file")
	flags.Int("parallel", -1, "Control max parallelism per project, -1 for unlimited")
	flags.Bool("compatibility", false, "Run compose in backward compatibility mode")
	flags.Bool("dry-run", false, "Execute command in dry run mode")
	flags.Bool("all-resources", false, "Include all resources, even those not used by services")

	var err error
	flags.VisitAll(func(f *pflag.Flag) {
		if bindErr := a.v.BindPFlag(f.Name, f); bindErr != nil {
			err = bindErr
		}
	})
	return err
}

// loadConfigFile reads $XDG_CONFIG_HOME/mdc/config.yaml when it exists.
func (a *App) loadConfigFile() error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil // no config directory on this system; flags and env still apply
	}
	a.v.SetConfigFile(filepath.Join(dir, "mdc", "config.yaml"))
	if err := a.v.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config file: %w", err)
	}
	return nil
}

// Depth is how many directory levels below the working directory are scanned.
func (a *App) Depth() int { return a.v.GetInt("depth") }

// Jobs is the maximum number of projects run at once; 0 means all.
func (a *App) Jobs() int { return a.v.GetInt("jobs") }

// BackendOptions returns the compose-level settings for backend.New.
func (a *App) BackendOptions() backend.Options {
	return backend.Options{
		Profiles:      a.v.GetStringSlice("profile"),
		EnvFiles:      a.v.GetStringSlice("env-file"),
		Parallel:      a.v.GetInt("parallel"),
		Compatibility: a.v.GetBool("compatibility"),
		DryRun:        a.v.GetBool("dry-run"),
		AllResources:  a.v.GetBool("all-resources"),
	}
}

// Discover finds the compose projects under the working directory.
func (a *App) Discover() (discover.Result, error) {
	root, err := os.Getwd()
	if err != nil {
		return discover.Result{}, err
	}
	res, err := discover.Targets(root, a.Depth())
	if err != nil {
		return discover.Result{}, err
	}
	if len(res.Targets) == 0 {
		return discover.Result{}, fmt.Errorf("no compose files found in %s (depth %d)", root, a.Depth())
	}
	return res, nil
}

// CommandFactory builds a subcommand that uses the shared App.
type CommandFactory func(app *App) *cobra.Command

var factories []CommandFactory

// Register adds a subcommand to the root command. Call it from init.
func Register(f CommandFactory) { factories = append(factories, f) }

package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"
	"github.com/docker/compose/v5/pkg/utils"
	"github.com/spf13/pflag"
)

const (
	envRemoveOrphans = "COMPOSE_REMOVE_ORPHANS"
	envIgnoreOrphans = "COMPOSE_IGNORE_ORPHANS"
)

// envBool reads a compose boolean environment variable.
func envBool(name string) bool { return utils.StringToBool(os.Getenv(name)) }

// optionalTimeout returns the timeout only when the flag was given, leaving
// compose to use each service's own stop grace period otherwise.
func optionalTimeout(seconds int, changed bool) *time.Duration {
	if !changed {
		return nil
	}
	d := time.Duration(seconds) * time.Second
	return &d
}

// waitTimeout converts --wait-timeout seconds; zero means no limit.
func waitTimeout(seconds int) time.Duration {
	return time.Duration(max(seconds, 0)) * time.Second
}

// normalizeYes accepts the --y misspelling of --yes.
func normalizeYes(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	if name == "y" {
		name = "yes"
	}
	return pflag.NormalizedName(name)
}

// createOptions are the flags shared by create and up.
type createOptions struct {
	build         bool
	noBuild       bool
	pull          string
	pullChanged   bool
	removeOrphans bool
	forceRecreate bool
	noRecreate    bool
	recreateDeps  bool
	noInherit     bool
	timeChanged   bool
	timeout       int
	quietPull     bool
	scale         []string
	assumeYes     bool
}

// addCreateFlags defines the flags create and up have in common.
func addCreateFlags(flags *pflag.FlagSet, o *createOptions, pullUsage string) {
	flags.BoolVar(&o.build, "build", false, "Build images before starting containers")
	flags.BoolVar(&o.noBuild, "no-build", false, "Don't build an image, even if it's policy")
	flags.StringVar(&o.pull, "pull", "policy", pullUsage)
	flags.BoolVar(&o.quietPull, "quiet-pull", false, "Pull without printing progress information")
	flags.BoolVar(&o.forceRecreate, "force-recreate", false, "Recreate containers even if their configuration and image haven't changed")
	flags.BoolVar(&o.noRecreate, "no-recreate", false, "If containers already exist, don't recreate them. Incompatible with --force-recreate.")
	flags.BoolVar(&o.removeOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	flags.StringArrayVar(&o.scale, "scale", []string{}, "Scale SERVICE to NUM instances. Overrides the `scale` setting in the Compose file if present.")
	flags.BoolVarP(&o.assumeYes, "yes", "y", false, `Assume "yes" as answer to all prompts and run non-interactively`)
	flags.SetNormalizeFunc(normalizeYes)
}

func (o createOptions) recreateStrategy() string {
	switch {
	case o.noRecreate:
		return api.RecreateNever
	case o.forceRecreate, o.noInherit:
		return api.RecreateForce
	}
	return api.RecreateDiverged
}

func (o createOptions) dependenciesRecreateStrategy() string {
	switch {
	case o.noRecreate:
		return api.RecreateNever
	case o.recreateDeps:
		return api.RecreateForce
	}
	return api.RecreateDiverged
}

// apiOptions maps the flags to compose's create options for one project.
func (o createOptions) apiOptions(project *types.Project, services []string, build *api.BuildOptions) api.CreateOptions {
	return api.CreateOptions{
		Build:                build,
		Services:             services,
		RemoveOrphans:        o.removeOrphans,
		IgnoreOrphans:        ignoreOrphans(project),
		Recreate:             o.recreateStrategy(),
		RecreateDependencies: o.dependenciesRecreateStrategy(),
		Inherit:              !o.noInherit,
		Timeout:              optionalTimeout(o.timeout, o.timeChanged),
		QuietPull:            o.quietPull,
	}
}

func ignoreOrphans(project *types.Project) bool {
	return utils.StringToBool(project.Environment[envIgnoreOrphans])
}

// buildOptions returns the build settings create and up use for images they
// need to build, or nil when --no-build is set. Build output is not shown.
func (o createOptions) buildOptions(services []string, deps bool) *api.BuildOptions {
	if o.noBuild {
		return nil
	}
	return &api.BuildOptions{
		Progress: "quiet",
		Quiet:    true,
		Services: services,
		Deps:     deps,
		Builder:  os.Getenv("BUILDX_BUILDER"),
	}
}

var validPullPolicies = []string{
	types.PullPolicyAlways, types.PullPolicyNever, types.PullPolicyBuild,
	types.PullPolicyMissing, types.PullPolicyIfNotPresent,
}

// validate rejects incompatible flags before any project is loaded.
func (o createOptions) validate() error {
	switch {
	case o.build && o.noBuild:
		return errors.New("--build and --no-build are incompatible")
	case o.forceRecreate && o.noRecreate:
		return errors.New("--force-recreate and --no-recreate are incompatible")
	case o.pullChanged && !slices.Contains(validPullPolicies, o.pull):
		return fmt.Errorf("invalid --pull option %q", o.pull)
	}
	return nil
}

// apply updates the project with the flags, as compose does before creating.
func (o createOptions) apply(project *types.Project) error {
	if o.removeOrphans && ignoreOrphans(project) {
		return fmt.Errorf("cannot combine %s and --remove-orphans", envIgnoreOrphans)
	}
	if o.pullChanged {
		for name, service := range project.Services {
			service.PullPolicy = o.pull
			project.Services[name] = service
		}
	}
	if o.build {
		for name, service := range project.Services {
			if service.Build == nil {
				continue
			}
			service.PullPolicy = types.PullPolicyBuild
			project.Services[name] = service
		}
	}
	if err := applyPlatforms(project); err != nil {
		return err
	}
	for _, spec := range o.scale {
		name, val, ok := strings.Cut(spec, "=")
		if !ok || val == "" {
			return fmt.Errorf("invalid --scale option %q. Should be SERVICE=NUM", spec)
		}
		replicas, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		service, ok := project.Services[name]
		if !ok {
			continue // defined by another project
		}
		service.SetScale(replicas)
		project.Services[name] = service
	}
	return nil
}

// applyPlatforms resolves DOCKER_DEFAULT_PLATFORM into services that build an
// image, and pins builds to the single platform they will run on.
func applyPlatforms(project *types.Project) error {
	defaultPlatform := project.Environment["DOCKER_DEFAULT_PLATFORM"]
	for name, service := range project.Services {
		if service.Build == nil {
			continue
		}
		if defaultPlatform != "" && service.Platform == "" {
			if len(service.Build.Platforms) > 0 && !slices.Contains(service.Build.Platforms, defaultPlatform) {
				return fmt.Errorf("service %q build.platforms does not support value set by DOCKER_DEFAULT_PLATFORM: %s", name, defaultPlatform)
			}
			service.Platform = defaultPlatform
		}
		if service.Platform != "" {
			if len(service.Build.Platforms) > 0 && !slices.Contains(service.Build.Platforms, service.Platform) {
				return fmt.Errorf("service %q build configuration does not support platform: %s", name, service.Platform)
			}
			service.Build.Platforms = []string{service.Platform}
		}
		if len(service.Build.Platforms) > 1 {
			// Running needs one platform; let the builder decide.
			service.Build.Platforms = nil
		}
		project.Services[name] = service
	}
	return nil
}

// rejectScheduledJobs refuses projects declaring active scheduled jobs, which
// compose cannot schedule.
func rejectScheduledJobs(project *types.Project) error {
	var names []string
	for name, job := range project.Jobs {
		if job.Triggers != nil && len(job.Triggers.Schedule) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return fmt.Errorf("scheduled jobs are not supported in this version: %s", strings.Join(names, ", "))
}

// runOptions selects services and applies --yes to the compose prompt.
func (o createOptions) runOptions(services []string) runOptions {
	opts := runOptions{Services: services}
	if o.assumeYes {
		opts.Compose = append(opts.Compose, compose.WithPrompt(compose.AlwaysOkPrompt()))
	}
	return opts
}

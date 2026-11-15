// Package backend adapts the compose SDK for running many projects at once.
package backend

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/flags"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"
	"github.com/moby/moby/client"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/version"
)

// Options are the compose-level settings shared by every target.
type Options struct {
	Profiles      []string
	EnvFiles      []string
	Parallel      int
	Compatibility bool
	DryRun        bool
	AllResources  bool
}

// Backend holds a single Docker CLI shared by all targets.
type Backend struct {
	cli    command.Cli
	opts   Options
	loader api.ProjectLoader
}

// New connects to the Docker engine described by the environment and Docker config.
func New(opts Options) (*Backend, error) {
	dockerCli, err := command.NewDockerCli(command.WithUserAgent("mdc/" + version.Version))
	if err != nil {
		return nil, fmt.Errorf("create docker cli: %w", err)
	}
	if err := dockerCli.Initialize(flags.NewClientOptions()); err != nil {
		return nil, fmt.Errorf("initialize docker cli: %w", err)
	}
	loader, err := compose.NewComposeService(dockerCli)
	if err != nil {
		return nil, fmt.Errorf("create compose service: %w", err)
	}
	return &Backend{cli: dockerCli, opts: opts, loader: loader}, nil
}

// Client is the Docker engine client shared by all targets.
func (b *Backend) Client() client.APIClient { return b.cli.Client() }

// Service returns a compose service dedicated to one target, reporting progress
// to events and asking questions through prompt. Extra options are applied last.
func (b *Backend) Service(events api.EventProcessor, prompt compose.Prompt, extra ...compose.Option) (api.Compose, error) {
	options := []compose.Option{
		compose.WithEventProcessor(events),
		compose.WithPrompt(prompt),
		// Anything compose prints directly would tear the live view; results
		// are reported through events and returned errors.
		compose.WithStreams(io.Discard, io.Discard, nil),
	}
	if b.opts.Parallel > 0 {
		options = append(options, compose.WithMaxConcurrency(b.opts.Parallel))
	}
	if b.opts.DryRun {
		options = append(options, compose.WithDryRun)
	}
	return compose.NewComposeService(b.cli, append(options, extra...)...)
}

// LoadProject loads the target's compose project. When services are given, the
// project is narrowed to them and their dependencies and the profiles they
// need are enabled.
func (b *Backend) LoadProject(ctx context.Context, t discover.Target, services ...string) (*types.Project, error) {
	return b.LoadProjectWith(ctx, t, nil, services...)
}

// LoadProjectWith is LoadProject with extra compose-go loader options.
func (b *Backend) LoadProjectWith(ctx context.Context, t discover.Target, fns []cli.ProjectOptionsFn, services ...string) (*types.Project, error) {
	return b.loader.LoadProject(ctx, api.ProjectLoadOptions{
		ProjectOptionsFns: fns,
		ProjectName:       t.ProjectName,
		ConfigPaths:       []string{t.File},
		WorkingDir:        t.Dir,
		EnvFiles:          b.opts.EnvFiles,
		Profiles:          b.opts.Profiles,
		Services:          services,
		All:               b.opts.AllResources,
		Compatibility:     b.opts.Compatibility,
	})
}

// LoadModel loads the target's compose model as a raw map, without building a
// project from it, so options such as skipping interpolation keep the file's
// own values.
func (b *Backend) LoadModel(ctx context.Context, t discover.Target, fns ...cli.ProjectOptionsFn) (map[string]any, error) {
	fns = append(slices.Clone(fns),
		cli.WithWorkingDirectory(t.Dir),
		cli.WithOsEnv,
		cli.WithEnvFiles(b.opts.EnvFiles...),
		cli.WithDotEnv,
		cli.WithDefaultProfiles(b.opts.Profiles...),
		cli.WithName(t.ProjectName),
	)
	options, err := cli.NewProjectOptions([]string{t.File}, fns...)
	if err != nil {
		return nil, err
	}
	return options.LoadModel(ctx)
}

// WithImageDigests pins the project's image tags to their registry digests.
func (b *Backend) WithImageDigests(ctx context.Context, project *types.Project) (*types.Project, error) {
	return project.WithImagesResolved(compose.ImageDigestResolver(ctx, b.cli.ConfigFile(), b.cli.Client()))
}

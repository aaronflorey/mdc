// Package backend adapts the compose SDK for running many projects at once.
package backend

import (
	"context"
	"fmt"
	"io"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/flags"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"

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
	return b.loader.LoadProject(ctx, api.ProjectLoadOptions{
		ProjectName:   t.ProjectName,
		ConfigPaths:   []string{t.File},
		WorkingDir:    t.Dir,
		EnvFiles:      b.opts.EnvFiles,
		Profiles:      b.opts.Profiles,
		Services:      services,
		All:           b.opts.AllResources,
		Compatibility: b.opts.Compatibility,
	})
}

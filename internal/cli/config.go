package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/aaronflorey/mdc/internal/ui"
)

func init() { Register(newConfigCommand) }

type configOptions struct {
	format         string
	output         string
	quiet          bool
	resolveDigests bool
	lockDigests    bool
	noInterpolate  bool
	noNormalize    bool
	noResolvePath  bool
	noResolveEnv   bool
	noConsistency  bool
	services       bool
	volumes        bool
	networks       bool
	models         bool
	profiles       bool
	images         bool
	hash           string
	variables      bool
	environment    bool
}

// selectsOutput reports whether an output-selecting flag replaces the rendered model.
func (o configOptions) selectsOutput() bool {
	return o.services || o.volumes || o.networks || o.models || o.hash != "" ||
		o.profiles || o.images || o.variables || o.environment
}

func newConfigCommand(app *App) *cobra.Command {
	var opts configOptions
	cmd := &cobra.Command{
		Use:   "config [OPTIONS] [SERVICE...]",
		Short: "Parse, resolve and render compose file in canonical format",
		PreRunE: func(*cobra.Command, []string) error {
			allowed := []string{"yaml", "json"}
			if opts.variables {
				allowed = append(allowed, "table")
			}
			if opts.format != "" {
				if err := outputFormat(opts.format, allowed...); err != nil {
					return err
				}
			}
			if app.BackendOptions().Compatibility {
				opts.noNormalize = true
			}
			if opts.lockDigests {
				opts.resolveDigests = true
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfig(cmd.Context(), app, args, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.format, "format", "", "Format the output. Values: [yaml | json]")
	flags.BoolVar(&opts.resolveDigests, "resolve-image-digests", false, "Pin image tags to digests")
	flags.BoolVar(&opts.lockDigests, "lock-image-digests", false, "Produces an override file with image digests")
	flags.BoolVarP(&opts.quiet, "quiet", "q", false, "Only validate the configuration, don't print anything")
	flags.BoolVar(&opts.noInterpolate, "no-interpolate", false, "Don't interpolate environment variables")
	flags.BoolVar(&opts.noNormalize, "no-normalize", false, "Don't normalize compose model")
	flags.BoolVar(&opts.noResolvePath, "no-path-resolution", false, "Don't resolve file paths")
	flags.BoolVar(&opts.noConsistency, "no-consistency", false, "Don't check model consistency - warning: may produce invalid Compose output")
	flags.BoolVar(&opts.noResolveEnv, "no-env-resolution", false, "Don't resolve service env files")
	flags.BoolVar(&opts.services, "services", false, "Print the service names, one per line.")
	flags.BoolVar(&opts.volumes, "volumes", false, "Print the volume names, one per line.")
	flags.BoolVar(&opts.networks, "networks", false, "Print the network names, one per line.")
	flags.BoolVar(&opts.models, "models", false, "Print the model names, one per line.")
	flags.BoolVar(&opts.profiles, "profiles", false, "Print the profile names, one per line.")
	flags.BoolVar(&opts.images, "images", false, "Print the image names, one per line.")
	flags.StringVar(&opts.hash, "hash", "", "Print the service config hash, one per line.")
	flags.BoolVar(&opts.variables, "variables", false, "Print model variables and default values.")
	flags.BoolVar(&opts.environment, "environment", false, "Print environment used for interpolation.")
	flags.StringVarP(&opts.output, "output", "o", "", "Save to file, relative to each project directory (default to stdout)")
	return cmd
}

func runConfig(ctx context.Context, app *App, args []string, opts configOptions) error {
	if opts.hash != "" {
		return errors.New("--hash is not supported: compose does not expose its service config hash to other programs")
	}
	if opts.noInterpolate && opts.resolveDigests {
		return errors.New("--no-interpolate cannot be combined with --resolve-image-digests or --lock-image-digests")
	}
	if opts.output != "" && filepath.IsAbs(opts.output) {
		return errors.New("--output must be a relative path: it is written inside each project directory")
	}

	snaps, err := collect(ctx, app, runOptions{Services: args, WholeProject: true}, func(ctx context.Context, r projectRun) (string, error) {
		content, err := opts.render(ctx, r)
		if err != nil || opts.output == "" || content == "" {
			return content, err
		}
		return "", os.WriteFile(filepath.Join(r.Target.Dir, opts.output), []byte(content+"\n"), 0o666)
	})
	if err != nil {
		return err
	}

	if !opts.quiet {
		if err := printConfig(succeeded(snaps), opts); err != nil {
			return err
		}
	}
	return reportOutcomes(outcomesOf(snaps), !opts.quiet)
}

func printConfig(snaps []snapshot[string], opts configOptions) error {
	if opts.format == "json" && !opts.selectsOutput() {
		byProject := map[string]json.RawMessage{}
		for _, s := range snaps {
			if s.Value != "" {
				byProject[s.Target.ProjectName] = json.RawMessage(s.Value)
			}
		}
		if len(byProject) == 0 {
			return nil
		}
		return printJSON(byProject)
	}
	var sections []string
	for _, s := range snaps {
		if s.Value != "" {
			sections = append(sections, ui.Section(s.Target, s.Value))
		}
	}
	if len(sections) > 0 {
		printLine(os.Stdout, strings.Join(sections, "\n\n"))
	}
	return nil
}

func (o configOptions) loaderOptions(extra ...cli.ProjectOptionsFn) []cli.ProjectOptionsFn {
	fns := []cli.ProjectOptionsFn{
		cli.WithInterpolation(!o.noInterpolate),
		cli.WithResolvedPaths(!o.noResolvePath),
		cli.WithNormalization(!o.noNormalize),
		cli.WithConsistency(!o.noConsistency),
		cli.WithDiscardEnvFile,
	}
	if o.noResolveEnv {
		fns = append(fns, cli.WithoutEnvironmentResolution)
	}
	return append(fns, extra...)
}

func (o configOptions) loadProject(ctx context.Context, r projectRun, services []string, extra ...cli.ProjectOptionsFn) (*types.Project, error) {
	return r.Backend.LoadProjectWith(ctx, r.Target, o.loaderOptions(extra...), services...)
}

func (o configOptions) loadModel(ctx context.Context, r projectRun, extra ...cli.ProjectOptionsFn) (map[string]any, error) {
	return r.Backend.LoadModel(ctx, r.Target, o.loaderOptions(extra...)...)
}

// render produces the text shown for one project.
func (o configOptions) render(ctx context.Context, r projectRun) (string, error) {
	switch {
	case o.services:
		return o.renderServices(ctx, r)
	case o.volumes:
		project, err := o.loadProject(ctx, r, nil, cli.WithoutEnvironmentResolution)
		return lines(slices.Sorted(maps.Keys(project.Volumes))), err
	case o.networks:
		project, err := o.loadProject(ctx, r, nil, cli.WithoutEnvironmentResolution)
		return lines(slices.Sorted(maps.Keys(project.Networks))), err
	case o.models:
		project, err := o.loadProject(ctx, r, nil, cli.WithoutEnvironmentResolution)
		var names []string
		for _, m := range project.Models {
			if m.Model != "" {
				names = append(names, m.Model)
			}
		}
		slices.Sort(names)
		return lines(names), err
	case o.profiles:
		return o.renderProfiles(ctx, r)
	case o.images:
		return o.renderImages(ctx, r)
	case o.variables:
		return o.renderVariables(ctx, r)
	case o.environment:
		project, err := o.loadProject(ctx, r, r.Services)
		return lines(slices.Sorted(slices.Values(project.Environment.Values()))), err
	}
	return o.renderModel(ctx, r)
}

func lines(items []string) string { return strings.Join(items, "\n") }

func (o configOptions) renderServices(ctx context.Context, r projectRun) (string, error) {
	if o.noInterpolate {
		model, err := o.loadModel(ctx, r, cli.WithoutEnvironmentResolution)
		if err != nil {
			return "", err
		}
		services, _ := model["services"].(map[string]any)
		return lines(slices.Sorted(maps.Keys(services))), nil
	}
	project, err := o.loadProject(ctx, r, nil, cli.WithoutEnvironmentResolution)
	if err != nil {
		return "", err
	}
	return lines(project.ServiceNames()), nil
}

func (o configOptions) renderProfiles(ctx context.Context, r projectRun) (string, error) {
	project, err := o.loadProject(ctx, r, r.Services)
	if err != nil {
		return "", err
	}
	set := map[string]struct{}{}
	for _, s := range project.AllServices() {
		for _, p := range s.Profiles {
			set[p] = struct{}{}
		}
	}
	return lines(slices.Sorted(maps.Keys(set))), nil
}

func (o configOptions) renderImages(ctx context.Context, r projectRun) (string, error) {
	project, err := o.loadProject(ctx, r, r.Services)
	if err != nil {
		return "", err
	}
	var images []string
	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		s := project.Services[name]
		images = append(images, api.GetImageNameOrDefault(s, project.Name))
		images = append(images, api.GetDependentImages(s, project.Name)...)
	}
	for _, name := range slices.Sorted(maps.Keys(project.Jobs)) {
		job := types.ServiceConfig{Name: name, ContainerSpec: types.ContainerSpec{Image: project.Jobs[name].Image}}
		images = append(images, api.GetImageNameOrDefault(job, project.Name))
		images = append(images, api.GetDependentImages(job, project.Name)...)
	}
	return lines(images), nil
}

func (o configOptions) renderVariables(ctx context.Context, r projectRun) (string, error) {
	o.noInterpolate = true
	model, err := o.loadModel(ctx, r, cli.WithoutEnvironmentResolution, cli.WithLoadOptions(loader.WithSkipValidation))
	if err != nil {
		return "", err
	}
	variables := template.ExtractVariables(model, template.DefaultPattern)
	switch o.format {
	case "yaml":
		out, err := yaml.Marshal(variables)
		return strings.TrimRight(string(out), "\n"), err
	case "json":
		out, err := json.MarshalIndent(variables, "", "  ")
		return string(out), err
	}
	if len(variables) == 0 {
		return "", nil
	}
	cols := []ui.Column{{Title: "NAME"}, {Title: "REQUIRED"}, {Title: "DEFAULT VALUE"}, {Title: "ALTERNATE VALUE"}}
	var rows [][]string
	for _, name := range slices.Sorted(maps.Keys(variables)) {
		v := variables[name]
		rows = append(rows, []string{name, fmt.Sprint(v.Required), v.DefaultValue, v.PresenceValue})
	}
	return ui.RenderTable(cols, rows, ui.TerminalWidth()), nil
}

// renderModel renders the whole resolved model as YAML or JSON.
func (o configOptions) renderModel(ctx context.Context, r projectRun) (string, error) {
	format := o.format
	if format == "" {
		format = "yaml"
	}
	var content []byte
	var err error
	if o.noInterpolate {
		var model map[string]any
		if model, err = o.loadModel(ctx, r); err != nil {
			return "", err
		}
		content, err = formatModel(model, format)
	} else {
		content, err = o.interpolatedModel(ctx, r, format)
		// Escape what compose would otherwise interpolate again when the output is reused.
		content = bytes.ReplaceAll(content, []byte{'$'}, []byte{'$', '$'})
	}
	return strings.TrimRight(string(content), "\n"), err
}

func (o configOptions) interpolatedModel(ctx context.Context, r projectRun, format string) ([]byte, error) {
	project, err := o.loadProject(ctx, r, r.Services)
	if err != nil {
		return nil, err
	}
	if o.resolveDigests {
		if project, err = r.Backend.WithImageDigests(ctx, project); err != nil {
			return nil, err
		}
	}
	if !o.noResolveEnv {
		if project, err = project.WithServicesEnvironmentResolved(true); err != nil {
			return nil, err
		}
	}
	if !o.noConsistency {
		if err := project.CheckContainerNameUnicity(); err != nil {
			return nil, err
		}
	}
	if o.lockDigests {
		project = imagesOnly(project)
	}
	if format == "json" {
		return project.MarshalJSON()
	}
	return project.MarshalYAML()
}

// imagesOnly strips a project down to service images and `type: image` volumes.
func imagesOnly(project *types.Project) *types.Project {
	pinned := func(spec types.ContainerSpec, volumes []types.ServiceVolumeConfig) (types.ContainerSpec, []types.ServiceVolumeConfig) {
		var kept []types.ServiceVolumeConfig
		for _, v := range volumes {
			if v.Type == types.VolumeTypeImage {
				kept = append(kept, v)
			}
		}
		return types.ContainerSpec{Image: spec.Image}, kept
	}
	services := types.Services{}
	for name, s := range project.Services {
		var service types.ServiceConfig
		service.ContainerSpec, service.Volumes = pinned(s.ContainerSpec, s.Volumes)
		services[name] = service
	}
	var jobs types.Jobs
	if len(project.Jobs) > 0 {
		jobs = types.Jobs{}
	}
	for name, j := range project.Jobs {
		var job types.JobConfig
		job.ContainerSpec, job.Volumes = pinned(j.ContainerSpec, j.Volumes)
		jobs[name] = job
	}
	return &types.Project{Services: services, Jobs: jobs}
}

func formatModel(model map[string]any, format string) ([]byte, error) {
	if format == "json" {
		return json.MarshalIndent(model, "", "  ")
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	err := encoder.Encode(model)
	return buf.Bytes(), err
}

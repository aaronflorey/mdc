package cli

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/types"
	cliopts "github.com/docker/cli/opts"
	"github.com/docker/compose/v5/pkg/api"
	"github.com/docker/compose/v5/pkg/compose"
	"github.com/spf13/cobra"

	"github.com/aaronflorey/mdc/internal/runner"
)

func init() { Register(newBuildCommand) }

// maxBuildLogBytes bounds how much of a build's output is kept for errors.
const maxBuildLogBytes = 8 << 10

// maxBuildLogLines is how much of a failed build's output the error includes.
const maxBuildLogLines = 15

// buildLog keeps the end of a build's plain-text output. BuildKit's own
// rendering would tear the live view, so it is captured here and only shown
// when the build fails.
type buildLog struct {
	mu  sync.Mutex
	buf []byte
}

func (l *buildLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if extra := len(l.buf) - maxBuildLogBytes; extra > 0 {
		l.buf = l.buf[extra:]
	}
	return len(p), nil
}

// failure adds the captured output to err when the build reported an error.
func (l *buildLog) failure(err error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !bytes.Contains(l.buf, []byte("ERROR")) {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(l.buf)), "\n")
	return fmt.Errorf("%w\n%s", err, strings.Join(lines[max(len(lines)-maxBuildLogLines, 0):], "\n"))
}

// captureBuildLog routes the build output into the error returned by run.
func captureBuildLog(build *api.BuildOptions, run func() error) error {
	if build == nil {
		return run()
	}
	log := &buildLog{}
	build.Progress = "plain"
	build.Out = log
	if err := run(); err != nil {
		return log.failure(err)
	}
	return nil
}

type buildOptions struct {
	quiet      bool
	pull       bool
	push       bool
	args       []string
	noCache    bool
	memory     cliopts.MemBytes
	ssh        string
	sshChanged bool
	builder    string
	deps       bool
	print      bool
	check      bool
	sbom       string
	provenance string
}

func newBuildCommand(app *App) *cobra.Command {
	var opts buildOptions
	cmd := &cobra.Command{
		Use:   "build [OPTIONS] [SERVICE...]",
		Short: "Build or rebuild services",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.sshChanged = cmd.Flags().Changed("ssh")
			return opts.runAll(cmd.Context(), app, args)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.push, "push", false, "Push service images")
	flags.BoolVarP(&opts.quiet, "quiet", "q", false, "Suppress the build output")
	flags.BoolVar(&opts.pull, "pull", false, "Always attempt to pull a newer version of the image")
	flags.StringArrayVar(&opts.args, "build-arg", []string{}, "Set build-time variables for services")
	flags.StringVar(&opts.ssh, "ssh", "", "Set SSH authentications used when building service images. (use 'default' for using your default SSH Agent)")
	flags.StringVar(&opts.builder, "builder", "", "Set builder to use")
	flags.BoolVar(&opts.deps, "with-dependencies", false, "Also build dependencies (transitively)")
	flags.StringVar(&opts.provenance, "provenance", "", `Add a provenance attestation`)
	flags.StringVar(&opts.sbom, "sbom", "", `Add a SBOM attestation`)

	flags.Bool("parallel", true, "Build images in parallel. DEPRECATED")
	_ = flags.MarkHidden("parallel")
	flags.Bool("compress", true, "Compress the build context using gzip. DEPRECATED")
	_ = flags.MarkHidden("compress")
	flags.Bool("force-rm", true, "Always remove intermediate containers. DEPRECATED")
	_ = flags.MarkHidden("force-rm")
	flags.BoolVar(&opts.noCache, "no-cache", false, "Do not use cache when building the image")
	flags.Bool("no-rm", false, "Do not remove intermediate containers after a successful build. DEPRECATED")
	_ = flags.MarkHidden("no-rm")
	flags.VarP(&opts.memory, "memory", "m", "Set memory limit for the build container. Not supported by BuildKit.")
	flags.String("progress", "", "Set type of ui output (auto, tty, plain, json, quiet)")
	_ = flags.MarkHidden("progress")
	flags.BoolVar(&opts.print, "print", false, "Print equivalent bake file")
	flags.BoolVar(&opts.check, "check", false, "Check build configuration")
	return cmd
}

func (o buildOptions) apiOptions(services []string) (api.BuildOptions, error) {
	var sshKeys []types.SSHKey
	ssh := o.ssh
	if o.sshChanged && ssh == "" {
		ssh = "default"
	}
	if ssh != "" {
		id, path, found := strings.Cut(ssh, "=")
		if !found && id != "default" {
			return api.BuildOptions{}, fmt.Errorf("invalid ssh key %q", ssh)
		}
		sshKeys = append(sshKeys, types.SSHKey{ID: id, Path: path})
	}
	builder := o.builder
	if builder == "" {
		builder = os.Getenv("BUILDX_BUILDER")
	}
	return api.BuildOptions{
		Pull:         o.pull,
		Push:         o.push,
		Progress:     "plain",
		Args:         types.NewMappingWithEquals(o.args),
		NoCache:      o.noCache,
		Quiet:        o.quiet,
		Services:     services,
		Deps:         o.deps,
		Memory:       int64(o.memory),
		Print:        o.print,
		Check:        o.check,
		SSHs:         sshKeys,
		Builder:      builder,
		SBOM:         o.sbom,
		Provenance:   o.provenance,
		Attestations: true,
	}, nil
}

func (o buildOptions) runAll(ctx context.Context, app *App, services []string) error {
	// Validate the flags once rather than per project.
	if _, err := o.apiOptions(services); err != nil {
		return err
	}
	// Builds can depend on other services through additional_contexts, so the
	// whole project stays loaded.
	runOpts := runOptions{Services: services, WholeProject: true, AllResources: true}
	var printed printedConfigs
	err := app.runEach(ctx, runOpts, func(ctx context.Context, r projectRun) error {
		return o.run(ctx, r, &printed)
	})
	printed.flush()
	return err
}

func (o buildOptions) run(ctx context.Context, r projectRun, printed *printedConfigs) error {
	if !buildsAnything(r, o.deps) {
		return runner.Skip("nothing to build")
	}
	if err := applyPlatforms(r.Project, false); err != nil {
		return err
	}
	build, err := o.apiOptions(r.Services)
	if err != nil {
		return err
	}
	if o.print {
		return o.printConfig(ctx, r, build, printed)
	}
	return captureBuildLog(&build, func() error {
		return r.Compose.Build(ctx, r.Project, build)
	})
}

// printConfig renders the bake file of one project, which compose writes to
// its output stream, to be shown once the live view has finished.
func (o buildOptions) printConfig(ctx context.Context, r projectRun, build api.BuildOptions, printed *printedConfigs) error {
	var out bytes.Buffer
	svc, err := r.NewService(compose.WithStreams(&out, &out, nil))
	if err != nil {
		return err
	}
	if err := svc.Build(ctx, r.Project, build); err != nil {
		return err
	}
	printed.add(r.Project.Name, out.String())
	return nil
}

// buildsAnything reports whether a build was requested for any service that
// has a build section.
func buildsAnything(r projectRun, withDeps bool) bool {
	for name, service := range r.Project.Services {
		selected := len(r.Services) == 0 || withDeps || slices.Contains(r.Services, name)
		if selected && service.Build != nil {
			return true
		}
	}
	return false
}

// printedConfigs collects output to show after the live view ends.
type printedConfigs struct {
	mu     sync.Mutex
	byName map[string]string
}

func (p *printedConfigs) add(project, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byName == nil {
		p.byName = map[string]string{}
	}
	p.byName[project] = text
}

func (p *printedConfigs) flush() {
	for _, name := range slices.Sorted(maps.Keys(p.byName)) {
		_, _ = fmt.Fprintf(os.Stdout, "# %s\n%s", name, p.byName[name])
	}
}

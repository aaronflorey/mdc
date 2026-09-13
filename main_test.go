package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDiscoverTargetsFindsDepthOneComposeFiles(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "docker-compose.yml"))
	mustWriteFile(t, filepath.Join(root, "api", "nested", "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, ".git", "compose.yaml"))

	targets, err := discoverTargets(root, 1)
	if err != nil {
		t.Fatalf("discoverTargets returned error: %v", err)
	}

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}

	labels := []string{targets[0].Label, targets[1].Label}
	joined := strings.Join(labels, ",")
	if joined != ".,api" {
		t.Fatalf("unexpected labels: %s", joined)
	}

	projectNames := []string{targets[0].ProjectName, targets[1].ProjectName}
	wantProjectNames := []string{composeDefaultProjectName(root), "api"}
	if strings.Join(projectNames, ",") != strings.Join(wantProjectNames, ",") {
		t.Fatalf("unexpected project names: %v", projectNames)
	}
}

func TestDiscoverTargetsUsesCanonicalComposeFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yml"))
	mustWriteFile(t, filepath.Join(root, "docker-compose.yml"))

	targets, err := discoverTargets(root, 0)
	if err != nil {
		t.Fatalf("discoverTargets returned error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}

	if got := filepath.Base(targets[0].File); got != "compose.yml" {
		t.Fatalf("expected compose.yml to win precedence, got %s", got)
	}
}

func TestParseArgsPreservesDockerComposeArguments(t *testing.T) {
	opts, composeArgs, nextAction, err := parseArgs([]string{"--depth", "2", "pull", "--ignore-pull-failures"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if nextAction != actionRun {
		t.Fatalf("expected actionRun, got %v", nextAction)
	}
	if opts.depth != 2 {
		t.Fatalf("expected depth 2, got %d", opts.depth)
	}
	if strings.Join(composeArgs, " ") != "pull --ignore-pull-failures" {
		t.Fatalf("unexpected compose args: %v", composeArgs)
	}
}

func TestParseArgsAllowsLeadingDockerComposeFlags(t *testing.T) {
	opts, composeArgs, nextAction, err := parseArgs([]string{"--ansi", "never", "ps"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if nextAction != actionRun {
		t.Fatalf("expected actionRun, got %v", nextAction)
	}
	if opts.depth != defaultDepth {
		t.Fatalf("expected default depth %d, got %d", defaultDepth, opts.depth)
	}
	if strings.Join(composeArgs, " ") != "--ansi never ps" {
		t.Fatalf("unexpected compose args: %v", composeArgs)
	}
}

func TestShouldMergePSSkipsLeadingComposeFlags(t *testing.T) {
	if !shouldMergePS([]string{"--ansi", "never", "ps"}) {
		t.Fatal("expected top-level ps after compose flags to be merged")
	}
}

func TestPSCommandIndexRecognizesTopLevelPS(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "bare ps", args: []string{"ps"}, want: 0},
		{name: "leading flag with value", args: []string{"--ansi", "never", "ps"}, want: 2},
		{name: "leading flag with equals", args: []string{"--ansi=never", "ps"}, want: 1},
		{name: "short flag with value", args: []string{"-p", "demo", "ps"}, want: 2},
		{name: "repeated file flags", args: []string{"-f", "a.yml", "-f", "b.yml", "ps"}, want: 4},
		{name: "long flags with values", args: []string{"--file", "compose.yml", "--project-directory", "./x", "ps"}, want: 4},
		{name: "long flags with equals", args: []string{"--file=compose.yml", "--project-directory=./x", "ps"}, want: 2},
		{name: "profile env parallel progress flags", args: []string{"--profile", "web", "--env-file", ".env", "--parallel", "2", "--progress", "tty", "ps"}, want: 8},
		{name: "first subcommand wins", args: []string{"up", "ps"}, want: -1},
		{name: "nested ps after flags", args: []string{"--ansi", "never", "exec", "app", "ps"}, want: -1},
		{name: "malformed file flag consumes ps", args: []string{"--file", "ps"}, want: -1},
		{name: "unknown leading flag stops parse", args: []string{"--unknown", "value", "ps"}, want: -1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := psCommandIndex(test.args); got != test.want {
				t.Fatalf("expected index %d, got %d for %v", test.want, got, test.args)
			}
		})
	}
}

func TestShouldMergePSRespectsFormatAndNesting(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "bare ps", args: []string{"ps"}, want: true},
		{name: "leading flag before ps", args: []string{"--ansi", "never", "ps"}, want: true},
		{name: "explicit non-json format", args: []string{"ps", "--format", "table"}, want: false},
		{name: "explicit json format with equals", args: []string{"ps", "--format=json"}, want: true},
		{name: "explicit json format case insensitive", args: []string{"ps", "--format", "JSON"}, want: true},
		{name: "malformed format flag", args: []string{"ps", "--format"}, want: false},
		{name: "nested ps under exec", args: []string{"exec", "app", "ps", "aux"}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldMergePS(test.args); got != test.want {
				t.Fatalf("expected merge=%t, got %t for %v", test.want, got, test.args)
			}
		})
	}
}

func TestComposeCommandRecognizesTopLevelSubcommand(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantCmd   string
		wantIndex int
	}{
		{name: "bare up", args: []string{"up", "-d"}, wantCmd: "up", wantIndex: 0},
		{name: "leading flags", args: []string{"--ansi", "never", "logs", "-f"}, wantCmd: "logs", wantIndex: 2},
		{name: "leading flags before events", args: []string{"--ansi", "never", "events"}, wantCmd: "events", wantIndex: 2},
		{name: "equals flags", args: []string{"--project-directory=demo", "pull"}, wantCmd: "pull", wantIndex: 1},
		{name: "unknown flag stops parse", args: []string{"--unknown", "value", "up"}, wantCmd: "", wantIndex: -1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCmd, gotIndex := composeCommand(test.args)
			if gotCmd != test.wantCmd || gotIndex != test.wantIndex {
				t.Fatalf("expected (%q, %d), got (%q, %d)", test.wantCmd, test.wantIndex, gotCmd, gotIndex)
			}
		})
	}
}

func TestOutputModeForArgsUsesCommandBehavior(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		jobs      int
		targets   int
		wantMode  outputMode
		wantError string
	}{
		{name: "parallel attached up interleaves", args: []string{"up"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "parallel detached up short flag buffers", args: []string{"up", "-d"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel detached up long flag buffers", args: []string{"up", "--detach"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel detached up long boolean flag buffers", args: []string{"up", "--detach=true"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel detached up short boolean flag buffers", args: []string{"up", "-d=true"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel false long detach flag interleaves", args: []string{"up", "--detach=false"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "parallel false short detach flag interleaves", args: []string{"up", "-d=false"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "parallel malformed detach flag interleaves", args: []string{"up", "--detach=perhaps"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "parallel detach after separator interleaves", args: []string{"up", "--", "--detach"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "serial attached up passes through", args: []string{"up"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "single-target attached up passes through", args: []string{"up"}, jobs: 0, targets: 1, wantMode: outputModePassthrough},
		{name: "parallel logs buffers without follow", args: []string{"logs", "--tail", "50"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel logs follow short flag interleaves", args: []string{"logs", "-f"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "parallel logs follow long flag interleaves", args: []string{"logs", "--follow"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "parallel logs with leading compose file buffers", args: []string{"-f", "compose.prod.yml", "logs", "--tail", "20"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel logs follow with leading compose file interleaves", args: []string{"-f", "compose.prod.yml", "logs", "-f"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "serial logs passes through", args: []string{"logs", "--tail", "20"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "single-target logs passes through", args: []string{"logs", "--tail", "20"}, jobs: 0, targets: 1, wantMode: outputModePassthrough},
		{name: "parallel build buffers", args: []string{"build"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "serial build passes through", args: []string{"build"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "single-target build passes through", args: []string{"build"}, jobs: 0, targets: 1, wantMode: outputModePassthrough},
		{name: "parallel build with leading compose flags buffers", args: []string{"--ansi", "never", "build"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "pull stays buffered", args: []string{"pull"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "config stays buffered", args: []string{"config"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel exec rejected", args: []string{"exec", "app", "sh"}, jobs: 0, targets: 2, wantError: "docker compose exec is interactive; rerun with --jobs 1"},
		{name: "parallel events rejected", args: []string{"events"}, jobs: 0, targets: 2, wantError: "docker compose events is live-streaming; rerun with --jobs 1"},
		{name: "leading flags events rejected", args: []string{"--ansi", "never", "events"}, jobs: 0, targets: 2, wantError: "docker compose events is live-streaming; rerun with --jobs 1"},
		{name: "serial events passthrough", args: []string{"events"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "parallel watch rejected", args: []string{"watch"}, jobs: 0, targets: 2, wantError: "docker compose watch is long-running; rerun with --jobs 1"},
		{name: "leading flags watch rejected", args: []string{"--ansi", "never", "watch"}, jobs: 0, targets: 2, wantError: "docker compose watch is long-running; rerun with --jobs 1"},
		{name: "serial watch passthrough", args: []string{"watch"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "single-target watch passthrough", args: []string{"watch"}, jobs: 0, targets: 1, wantMode: outputModePassthrough},
		{name: "parallel stats buffers", args: []string{"stats"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "serial stats passthrough", args: []string{"stats"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "single-target stats passthrough", args: []string{"stats"}, jobs: 0, targets: 1, wantMode: outputModePassthrough},
		{name: "parallel down with volumes buffers", args: []string{"down", "--volumes"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel restart buffers", args: []string{"restart"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "parallel top buffers", args: []string{"top"}, jobs: 0, targets: 2, wantMode: outputModeBuffered},
		{name: "serial top passthrough", args: []string{"top"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
		{name: "single-target top passthrough", args: []string{"top"}, jobs: 0, targets: 1, wantMode: outputModePassthrough},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotMode, err := outputModeForArgs(test.args, test.jobs, test.targets)
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("expected error %q, got %v", test.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMode != test.wantMode {
				t.Fatalf("expected mode %v, got %v", test.wantMode, gotMode)
			}
		})
	}
}

func TestSummarizeComposeStatusLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "image pulling", line: "Image ghcr.io/acme/app:latest Pulling", want: "pulling ghcr.io/acme/app:latest"},
		{name: "image pulled", line: "Image ghcr.io/acme/app:latest Pulled", want: "pulled ghcr.io/acme/app:latest"},
		{name: "container healthy", line: "Container tracearr-redis-1 Healthy", want: "tracearr-redis-1 Healthy"},
		{name: "layer downloading", line: "122955722495 Downloading 136.3MB", want: "122955722495 Downloading 136.3MB"},
		{name: "fallback", line: "some other line", want: "some other line"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := summarizeComposeStatusLine(test.line); got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}

func TestLiveStatusWriterBuffersRawOutputAndTracksLatestStatus(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoard(&dest, []target{{Label: "."}, {Label: "api"}})
	board.start()

	writer := &liveStatusWriter{board: board, target: target{Label: "api"}}
	input := "Image ghcr.io/acme/app:latest Pulling\r122955722495 Downloading 136.3MB\rContainer api-web-1 Started\n"
	if _, err := io.WriteString(writer, input); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()

	if got := writer.bufferedOutput(); got != input {
		t.Fatalf("expected raw output %q, got %q", input, got)
	}
	if got := board.statuses["api"]; got != "api-web-1 Started" {
		t.Fatalf("expected latest status to be tracked, got %q", got)
	}
	if !strings.Contains(dest.String(), "Running 2 compose projects...") {
		t.Fatalf("expected board header, got %q", dest.String())
	}
	if !strings.Contains(dest.String(), "[api]") {
		t.Fatalf("expected api row render, got %q", dest.String())
	}
}

func TestComposeUniqueProjectNameIsStableAndUnique(t *testing.T) {
	first := composeUniqueProjectName(filepath.Join(string(os.PathSeparator), "tmp", "services", "app"))
	second := composeUniqueProjectName(filepath.Join(string(os.PathSeparator), "srv", "examples", "app"))
	repeated := composeUniqueProjectName(filepath.Join(string(os.PathSeparator), "tmp", "services", "app"))

	if first != repeated {
		t.Fatalf("expected stable project name, got %q and %q", first, repeated)
	}
	if first == second {
		t.Fatalf("expected different project names for different directories, got %q", first)
	}
	if !regexp.MustCompile(`^mdc-[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]+$`).MatchString(first) {
		t.Fatalf("unexpected project name format: %q", first)
	}
}

func TestComposeDefaultProjectNameUsesDirectoryBasename(t *testing.T) {
	if got := composeDefaultProjectName(filepath.Join(string(os.PathSeparator), "tmp", "manual-demo")); got != "manual-demo" {
		t.Fatalf("expected manual-demo, got %q", got)
	}
}

func TestDiscoverTargetsDisambiguatesDuplicateProjectNames(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "services", "app")
	secondDir := filepath.Join(root, "examples", "app")
	mustWriteFile(t, filepath.Join(firstDir, "compose.yaml"))
	mustWriteFile(t, filepath.Join(secondDir, "compose.yaml"))

	targets, err := discoverTargets(root, 2)
	if err != nil {
		t.Fatalf("discoverTargets returned error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].ProjectName == targets[1].ProjectName {
		t.Fatalf("expected distinct project names, got %q", targets[0].ProjectName)
	}
	if !strings.HasPrefix(targets[0].ProjectName, "mdc-app-") || !strings.HasPrefix(targets[1].ProjectName, "mdc-app-") {
		t.Fatalf("expected hashed duplicate project names, got %q and %q", targets[0].ProjectName, targets[1].ProjectName)
	}
}

func TestComposeCommandArgsIncludeProjectName(t *testing.T) {
	dirOne := filepath.Join(string(os.PathSeparator), "tmp", "services", "app")
	dirTwo := filepath.Join(string(os.PathSeparator), "srv", "examples", "app")

	argsOne := composeCommandArgs(target{Dir: dirOne, File: filepath.Join(dirOne, "compose.yaml")}, []string{"up", "-d"})
	argsTwo := composeCommandArgs(target{Dir: dirTwo, File: filepath.Join(dirTwo, "compose.yaml"), ProjectName: composeUniqueProjectName(dirTwo)}, []string{"up", "-d"})

	projectOne := valueAfterFlag(argsOne, "--project-name")
	projectTwo := valueAfterFlag(argsTwo, "--project-name")
	if projectOne == "" || projectTwo == "" {
		t.Fatalf("expected project names in args, got %v and %v", argsOne, argsTwo)
	}
	if projectOne != "app" {
		t.Fatalf("expected default project name app, got %q", projectOne)
	}
	if projectTwo == projectOne {
		t.Fatalf("expected distinct project names, got %q", projectOne)
	}
	if got := strings.Join(argsOne[len(argsOne)-2:], " "); got != "up -d" {
		t.Fatalf("expected docker compose args at the end, got %v", argsOne)
	}
	if valueAfterFlag(argsOne, "-f") != filepath.Join(dirOne, "compose.yaml") {
		t.Fatalf("expected compose file in args, got %v", argsOne)
	}
}

func TestExecComposeRunsInTargetDirectory(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	targetDir := filepath.Join(root, "stack")
	composeFile := filepath.Join(targetDir, "compose.yaml")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", binDir, err)
	}
	mustWriteFile(t, composeFile)

	scriptPath := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\npwd\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", scriptPath, err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := execCompose(context.Background(), target{Dir: targetDir, File: composeFile, Label: "."}, []string{"up", "-d"}, &stdout, &stderr)

	if result.exitCode != 0 {
		t.Fatalf("expected success, got %+v", result)
	}
	lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected cwd and args in stdout, got %q", result.stdout)
	}
	if lines[0] != targetDir {
		t.Fatalf("expected cwd %q, got %q", targetDir, lines[0])
	}
	if strings.Join(lines[1:], " ") != strings.Join(composeCommandArgs(target{Dir: targetDir, File: composeFile}, []string{"up", "-d"}), " ") {
		t.Fatalf("unexpected args: %q", result.stdout)
	}
}

func TestExecComposeForwardsStdin(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	targetDir := filepath.Join(root, "stack")
	composeFile := filepath.Join(targetDir, "compose.yaml")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", binDir, err)
	}
	mustWriteFile(t, composeFile)

	scriptPath := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\nIFS= read -r line\nprintf 'stdin:%s\\n' \"$line\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", scriptPath, err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	originalStdin := os.Stdin
	t.Cleanup(func() {
		os.Stdin = originalStdin
	})
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinReader.Close()
	})
	if _, err := stdinWriter.WriteString("forwarded input\\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = stdinReader

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := execCompose(context.Background(), target{Dir: targetDir, File: composeFile, Label: "."}, []string{"exec", "app", "sh"}, &stdout, &stderr)

	if result.exitCode != 0 {
		t.Fatalf("expected success, got %+v", result)
	}
	if !strings.Contains(result.stdout, "stdin:forwarded input") {
		t.Fatalf("expected forwarded stdin in stdout, got %q", result.stdout)
	}
}

func TestShouldMergePSIgnoresNestedPSArguments(t *testing.T) {
	for _, args := range [][]string{{"exec", "app", "ps", "aux"}, {"run", "worker", "ps"}} {
		if shouldMergePS(args) {
			t.Fatalf("expected args %v to bypass merged ps handling", args)
		}
	}
}

func TestRunCLIReportsNonZeroWhenAnyTargetFails(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json up -d" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stack.Label == "api" {
			return commandResult{target: stack, stderr: "boom", exitCode: 3, err: fmt.Errorf("boom")}
		}
		return commandResult{target: stack, stdout: "ok", exitCode: 0}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"up", "-d"}, runner)

	if code != 3 {
		t.Fatalf("expected exit code 3, got %d", code)
	}
	if !strings.Contains(stdout.String(), "[.] up complete\n") {
		t.Fatalf("expected successful target summary, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "api (boom)") {
		t.Fatalf("expected failure summary, got %q", stderr.String())
	}
}

func TestRunCLIPropagatesContextCancellation(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var started atomic.Int32
	runner := func(ctx context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json up -d" {
			t.Fatalf("unexpected args: %v", args)
		}
		if started.Add(1) == 2 {
			cancel()
		}
		<-ctx.Done()
		return commandResult{target: stack, stderr: ctx.Err().Error(), exitCode: 130, err: ctx.Err()}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(ctx, &stdout, &stderr, []string{"up", "-d"}, runner)

	if code != 130 {
		t.Fatalf("expected exit code 130, got %d", code)
	}
	if started.Load() != 2 {
		t.Fatalf("expected both targets to start before cancellation, got %d", started.Load())
	}
	if !strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("expected cancellation in stderr, got %q", stderr.String())
	}
}

func TestRunCLIMergesPSJSONIntoSingleTable(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	responses := map[string]string{
		".":   `[{"ID":"1","Name":"root-web-1","Service":"web","State":"running","Health":"healthy","Publishers":[{"URL":"0.0.0.0","PublishedPort":8080,"TargetPort":80,"Protocol":"tcp"}]}]`,
		"api": `[{"ID":"2","Name":"api-web-1","Service":"web","State":"running","Publishers":[]}]`,
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if got := strings.Join(args, " "); got != "ps --format json" {
			t.Fatalf("unexpected args: %s", got)
		}
		return commandResult{target: stack, stdout: responses[stack.Label]}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"ps"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if strings.Count(output, "NAME") != 1 {
		t.Fatalf("expected a single header row, got %q", output)
	}
	if !strings.Contains(output, "root-web-1") || !strings.Contains(output, "api-web-1") {
		t.Fatalf("expected merged ps rows, got %q", output)
	}
	if !strings.Contains(output, "0.0.0.0:8080->80/tcp") {
		t.Fatalf("expected port mapping, got %q", output)
	}
}

func TestRunCLIFallsBackToTextPSWhenJSONFails(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var (
		mu    sync.Mutex
		calls []string
	)
	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		mu.Lock()
		calls = append(calls, stack.Label+":"+strings.Join(args, " "))
		mu.Unlock()
		if strings.Join(args, " ") == "ps --format json" {
			return commandResult{target: stack, stderr: "unsupported format", exitCode: 1, err: fmt.Errorf("unsupported format")}
		}
		return commandResult{target: stack, stdout: "NAME SERVICE STATUS\n" + stack.Label + " web running\n"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"ps"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if strings.Count(stdout.String(), "NAME SERVICE STATUS") != 1 {
		t.Fatalf("expected deduped fallback header, got %q", stdout.String())
	}

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 4 {
		t.Fatalf("expected json attempt plus fallback for each target, got %v", calls)
	}

	expectedCalls := map[string]int{
		".:ps --format json":   1,
		"api:ps --format json": 1,
		".:ps":                 1,
		"api:ps":               1,
	}
	for _, call := range calls {
		expectedCalls[call]--
	}
	for call, remaining := range expectedCalls {
		if remaining != 0 {
			t.Fatalf("expected call %q exactly once, got %v", call, calls)
		}
	}
}

func TestRunCLIMergesImagesOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "images" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stack.Label == "." {
			return commandResult{target: stack, stdout: "CONTAINER  IMAGE               COMMAND  CREATED      STATUS  PORTS\nroot-web   nginx:latest        sh       2 hours ago  Up 2h   80/tcp\n"}
		}
		return commandResult{target: stack, stdout: "CONTAINER  IMAGE               COMMAND  CREATED      STATUS  PORTS\napi-web    ghcr.io/acme/api:v1 sh       Just now     Up 1s   8080/tcp\n"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"images"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Count(out, "CONTAINER") != 1 {
		t.Fatalf("expected single header, got %q", out)
	}
	if !strings.Contains(out, "root-web") || !strings.Contains(out, "api-web") {
		t.Fatalf("expected merged rows, got %q", out)
	}
}

func TestRunCLIFallsBackToDeterministicImagesOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "zed", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "images" {
			t.Fatalf("unexpected args: %v", args)
		}
		switch stack.Label {
		case ".":
			return commandResult{target: stack, stdout: "CONTAINER IMAGE\nshared nginx\n"}
		case "api":
			return commandResult{target: stack, stdout: "CONTAINER  IMAGE\nshared nginx\n"}
		default:
			return commandResult{target: stack, stdout: "CONTAINER  IMAGE\nzed-web ghcr.io/acme/api:v1\n"}
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"images"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Count(out, "CONTAINER") != 1 {
		t.Fatalf("expected single retained header, got %q", out)
	}
	if strings.Count(out, "shared nginx") != 2 {
		t.Fatalf("expected conservative duplicate-row retention, got %q", out)
	}
	if !strings.Contains(out, "zed-web ghcr.io/acme/api:v1") {
		t.Fatalf("expected zed row in fallback output, got %q", out)
	}
	firstShared := strings.Index(out, "shared nginx")
	zedRow := strings.Index(out, "zed-web ghcr.io/acme/api:v1")
	if firstShared == -1 || zedRow == -1 || firstShared > zedRow {
		t.Fatalf("expected deterministic sorted fallback order, got %q", out)
	}
}

func TestRunCLIMergesImagesOutputWithLeadingComposeFlags(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "--ansi never images" {
			t.Fatalf("unexpected args: %v", args)
		}
		return commandResult{target: stack, stdout: "CONTAINER  IMAGE\n" + stack.Label + "-web nginx\n"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"--ansi", "never", "images"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Count(out, "CONTAINER") != 1 || !strings.Contains(out, ".-web") || !strings.Contains(out, "api-web") {
		t.Fatalf("expected merged top-level images output with leading flags, got %q", out)
	}
}

func TestRunCLIKeepsImagesFailureDetails(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if stack.Label == "api" {
			return commandResult{target: stack, stderr: "permission denied", exitCode: 4, err: fmt.Errorf("permission denied")}
		}
		return commandResult{target: stack, stdout: "CONTAINER IMAGE\nroot-web nginx\n"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"images"}, runner)
	if code != 4 {
		t.Fatalf("expected failure code 4, got %d", code)
	}
	if !strings.Contains(stdout.String(), "[api]\npermission denied") {
		t.Fatalf("expected failed target details, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (permission denied)") {
		t.Fatalf("expected failure summary, got %q", stderr.String())
	}
}

func TestRunCLIPassesThroughNestedImagesArguments(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var received []string
	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		received = append([]string(nil), args...)
		return commandResult{target: stack, stdout: "ok"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"exec", "app", "images"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if strings.Join(received, " ") != "exec app images" {
		t.Fatalf("expected passthrough args, got %v", received)
	}
}

func TestShouldMergeVolumes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "volumes", args: []string{"volumes"}, want: true},
		{name: "volumes with service", args: []string{"volumes", "web"}, want: true},
		{name: "leading flags", args: []string{"--ansi", "never", "volumes"}, want: true},
		{name: "volumes with format flag", args: []string{"volumes", "--format", "json"}, want: true},
		{name: "volumes with non-json format", args: []string{"volumes", "--format", "table"}, want: false},
		{name: "other command", args: []string{"ps"}, want: false},
		{name: "nested under exec", args: []string{"exec", "app", "volumes"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldMergeVolumes(test.args); got != test.want {
				t.Fatalf("expected merge=%t, got %t for %v", test.want, got, test.args)
			}
		})
	}
}

func TestVolumesJSONArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "volumes", args: []string{"volumes"}, want: "volumes --format json"},
		{name: "volumes with service", args: []string{"volumes", "web"}, want: "volumes --format json web"},
		{name: "explicit json format", args: []string{"volumes", "--format", "json"}, want: "volumes --format json"},
		{name: "explicit json format equals", args: []string{"volumes", "--format=json"}, want: "volumes --format=json"},
		{name: "other command", args: []string{"ps"}, want: "ps"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := volumesJSONArgs(test.args)
			if strings.Join(got, " ") != test.want {
				t.Fatalf("expected %q, got %q", test.want, strings.Join(got, " "))
			}
		})
	}
}

func TestRunCLIMergesVolumesOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "volumes --format json" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stack.Label == "." {
			return commandResult{target: stack, stdout: `[{"Name":"web_cache","Driver":"local","Scope":"local"}]`}
		}
		return commandResult{target: stack, stdout: `[{"Name":"db_data","Driver":"local","Scope":"local"}]`}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"volumes"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Count(out, "PROJECT") != 1 {
		t.Fatalf("expected single header, got %q", out)
	}
	if !strings.Contains(out, "web_cache") || !strings.Contains(out, "db_data") {
		t.Fatalf("expected merged volume rows, got %q", out)
	}
	if !regexp.MustCompile(`(?m)^\.\s+web_cache`).MatchString(out) || !regexp.MustCompile(`(?m)^api\s+db_data`).MatchString(out) {
		t.Fatalf("expected project column with target labels, got %q", out)
	}
}

func TestRunCLIVolumesFallsBackToText(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var (
		mu    sync.Mutex
		calls []string
	)
	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		mu.Lock()
		calls = append(calls, stack.Label+":"+strings.Join(args, " "))
		mu.Unlock()
		if strings.Join(args, " ") == "volumes --format json" {
			return commandResult{target: stack, stderr: "unsupported format", exitCode: 1, err: fmt.Errorf("unsupported format")}
		}
		return commandResult{target: stack, stdout: "DRIVER VOLUME NAME\nlocal " + stack.Label + "_cache\n"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"volumes"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if strings.Count(stdout.String(), "DRIVER") != 1 {
		t.Fatalf("expected deduped fallback header, got %q", stdout.String())
	}

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 4 {
		t.Fatalf("expected json attempt plus fallback for each target, got %v", calls)
	}
	expectedCalls := map[string]int{
		".:volumes --format json":   1,
		"api:volumes --format json": 1,
		".:volumes":                 1,
		"api:volumes":               1,
	}
	for _, call := range calls {
		expectedCalls[call]--
	}
	for call, remaining := range expectedCalls {
		if remaining != 0 {
			t.Fatalf("expected call %q exactly once, got %v", call, calls)
		}
	}
}

func TestRunCLIPassesThroughNestedPSArguments(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var received []string
	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		received = append([]string(nil), args...)
		return commandResult{target: stack, stdout: "ok"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"exec", "app", "ps", "aux"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if strings.Join(received, " ") != "exec app ps aux" {
		t.Fatalf("expected passthrough args, got %v", received)
	}
	if !strings.Contains(stdout.String(), "[.]\nok") {
		t.Fatalf("expected standard output, got %q", stdout.String())
	}
}

func TestRunCLIInterleavesTopLevelParallelAttachedUp(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json up" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stdout == nil || stderr == nil {
			recordRunnerErr(fmt.Errorf("expected live writers for attached up"))
			return commandResult{target: stack}
		}
		if _, err := io.WriteString(stdout, "up line\n"); err != nil {
			recordRunnerErr(fmt.Errorf("write stdout: %w", err))
		}
		return commandResult{target: stack, streamed: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"up"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[.] up line\n") || !strings.Contains(output, "[api] up line\n") {
		t.Fatalf("expected interleaved attached up output with per-target prefixes, got %q", output)
	}
	if strings.Contains(output, "up complete") {
		t.Fatalf("expected attached up output without summaries, got %q", output)
	}
}

func TestRunCLIBuffersParallelDetachedUpOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json up -d" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stdout != nil || stderr != nil {
			recordRunnerErr(fmt.Errorf("expected buffered up output"))
		}
		return commandResult{target: stack, stdout: "started"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"up", "-d"}, runner)

	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "started") {
		t.Fatalf("expected buffered success summary, got %q", output)
	}
	if !strings.Contains(output, "[.] up complete\n") {
		t.Fatalf("expected root target summary, got %q", output)
	}
	if !strings.Contains(output, "[api] up complete\n") {
		t.Fatalf("expected api target summary, got %q", output)
	}
}

func TestRunCLIKeepsUpFailureDetailsInBufferedMode(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json up -d" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stdout != nil || stderr != nil {
			recordRunnerErr(fmt.Errorf("expected buffered up output"))
		}
		if stack.Label == "api" {
			return commandResult{target: stack, stderr: "image pull failed", exitCode: 9, err: fmt.Errorf("image pull failed")}
		}
		return commandResult{target: stack, stdout: "started"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"up", "-d"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 9 {
		t.Fatalf("expected failure exit code, got %d", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "[.] up complete\n") {
		t.Fatalf("expected successful target summary, got %q", output)
	}
	if !strings.Contains(output, "[api]\nimage pull failed\n") {
		t.Fatalf("expected failed target stderr output, got %q", output)
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (image pull failed)") {
		t.Fatalf("expected up failure summary, got %q", stderr.String())
	}
}

func TestRunCLIBuffersTopLevelParallelLogsWithoutFollow(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "logs --tail 50" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stdout != nil || stderr != nil {
			recordRunnerErr(fmt.Errorf("expected buffered logs output for non-follow mode"))
		}
		return commandResult{target: stack, stdout: "history line"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"logs", "--tail", "50"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[.]\nhistory line\n") || !strings.Contains(output, "[api]\nhistory line\n") {
		t.Fatalf("expected grouped buffered logs output, got %q", output)
	}
}

func TestRunCLIBuffersTopLevelParallelTopInTargetOrder(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "zebra", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "top" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout != nil || stderr != nil {
			t.Fatalf("expected buffered top output")
		}
		return commandResult{target: stack, stdout: fmt.Sprintf("%s top line 1\n%s top line 2\n", stack.Label, stack.Label)}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"top"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}

	want := "[.]\n. top line 1\n. top line 2\n\n[api]\napi top line 1\napi top line 2\n\n[zebra]\nzebra top line 1\nzebra top line 2\n"
	if stdout.String() != want {
		t.Fatalf("expected grouped top output in target order, got %q", stdout.String())
	}
}

func TestRunCLIKeepsLogsFailureDetailsInBufferedMode(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "logs --tail 50" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stack.Label == "api" {
			return commandResult{target: stack, stderr: "log stream denied", exitCode: 4, err: fmt.Errorf("log stream denied")}
		}
		return commandResult{target: stack, stdout: "history line"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"logs", "--tail", "50"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 4 {
		t.Fatalf("expected failure exit code, got %d", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "[.]\nhistory line\n") {
		t.Fatalf("expected successful target grouped output, got %q", output)
	}
	if !strings.Contains(output, "[api]\nlog stream denied\n") {
		t.Fatalf("expected failed target stderr output, got %q", output)
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (log stream denied)") {
		t.Fatalf("expected logs failure summary, got %q", stderr.String())
	}
}

func TestRunCLIInterleavesTopLevelParallelLogsFollowWithComposeFlags(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--ansi never logs --follow" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stdout == nil || stderr == nil {
			recordRunnerErr(fmt.Errorf("expected live writers for logs follow mode"))
		}
		if _, err := io.WriteString(stdout, "tail line\n"); err != nil {
			recordRunnerErr(fmt.Errorf("write stdout: %w", err))
		}
		if _, err := io.WriteString(stderr, "warn line\n"); err != nil {
			recordRunnerErr(fmt.Errorf("write stderr: %w", err))
		}
		return commandResult{target: stack, streamed: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"--ansi", "never", "logs", "--follow"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[.] tail line\n") || !strings.Contains(output, "[api] tail line\n") {
		t.Fatalf("expected interleaved follow stdout output with per-target prefixes, got %q", output)
	}
	errOutput := stderr.String()
	if !strings.Contains(errOutput, "[.] warn line\n") || !strings.Contains(errOutput, "[api] warn line\n") {
		t.Fatalf("expected interleaved follow stderr output with per-target prefixes, got %q", errOutput)
	}
}

func TestRunCLILogsFollowFailureKeepsStreamingAndSummary(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "logs --follow" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if _, err := io.WriteString(stdout, "live line\n"); err != nil {
			recordRunnerErr(fmt.Errorf("write stdout: %w", err))
		}
		if stack.Label == "api" {
			return commandResult{target: stack, exitCode: 6, streamed: true, err: fmt.Errorf("follow failed")}
		}
		return commandResult{target: stack, streamed: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"logs", "--follow"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 6 {
		t.Fatalf("expected failure exit code, got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "[.] live line\n") || !strings.Contains(out, "[api] live line\n") {
		t.Fatalf("expected live prefixed follow output, got %q", out)
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (exit 6)") {
		t.Fatalf("expected follow failure summary, got %q", stderr.String())
	}
}

func TestRunCLIPreservesLogsPassthroughForSerialAndSingleTarget(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	tests := []struct {
		name string
		argv []string
	}{
		{name: "serial logs", argv: []string{"--jobs", "1", "logs", "--tail", "5"}},
		{name: "single target logs", argv: []string{"--depth", "0", "logs", "--tail", "5"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var runnerErr error
			var runnerErrMu sync.Mutex
			recordRunnerErr := func(err error) {
				runnerErrMu.Lock()
				defer runnerErrMu.Unlock()
				if runnerErr == nil {
					runnerErr = err
				}
			}

			runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
				if strings.Join(args, " ") != "logs --tail 5" {
					recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
				}
				if stdout == nil || stderr == nil {
					recordRunnerErr(fmt.Errorf("expected passthrough writers for logs"))
				}
				if _, err := io.WriteString(stdout, "plain line\n"); err != nil {
					recordRunnerErr(fmt.Errorf("write stdout: %w", err))
				}
				return commandResult{target: stack, streamed: true}
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := runCLI(context.Background(), &stdout, &stderr, tc.argv, runner)
			if runnerErr != nil {
				t.Fatalf("runner assertion failed: %v", runnerErr)
			}
			if code != 0 {
				t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
			}
			output := stdout.String()
			if strings.Contains(output, "[.] plain line") || strings.Contains(output, "[api] plain line") {
				t.Fatalf("expected passthrough logs output without target prefixes, got %q", output)
			}
			if tc.name == "serial logs" && strings.Count(output, "plain line") != 2 {
				t.Fatalf("expected output from both targets, got %q", output)
			}
			if tc.name == "single target logs" && strings.Count(output, "plain line") != 1 {
				t.Fatalf("expected output from one target, got %q", output)
			}
		})
	}
}

func TestRunCLIBuffersOnlyParallelBuildOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	tests := []struct {
		name         string
		argv         []string
		wantBuffered bool
	}{
		{name: "parallel build", argv: []string{"build"}, wantBuffered: true},
		{name: "bounded parallel build", argv: []string{"--jobs", "2", "build"}, wantBuffered: true},
		{name: "serial build", argv: []string{"--jobs", "1", "build"}, wantBuffered: false},
		{name: "single target build", argv: []string{"--depth", "0", "build"}, wantBuffered: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
				if got := strings.Join(args, " "); got != "build" && got != "--progress=json build" {
					t.Fatalf("unexpected args: %v", args)
				}
				isBuffered := stdout == nil && stderr == nil
				if isBuffered != test.wantBuffered {
					t.Fatalf("expected buffered=%t, got stdout=%T stderr=%T", test.wantBuffered, stdout, stderr)
				}
				if stdout != nil {
					if _, err := io.WriteString(stdout, "build output\n"); err != nil {
						t.Fatalf("write stdout: %v", err)
					}
				}
				return commandResult{target: stack, stdout: "build output"}
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := runCLI(context.Background(), &stdout, &stderr, test.argv, runner)
			if code != 0 {
				t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
			}
			if test.wantBuffered {
				return
			}
			if !strings.Contains(stdout.String(), "build output") {
				t.Fatalf("expected passthrough/interleaved output, got %q", stdout.String())
			}
			if strings.Contains(stdout.String(), "build complete") {
				t.Fatalf("expected non-buffered build output unchanged, got %q", stdout.String())
			}
		})
	}
}

func TestRunCLIHonorsQuietTargetsInBufferedOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	tests := []struct {
		name          string
		argv          []string
		wantHeaders   bool
		wantBodies    bool
		wantSummaries bool
	}{
		{
			name:       "generic output omits headers when quiet",
			argv:       []string{"--quiet-targets", "config"},
			wantBodies: true,
		},
		{
			name:        "generic output keeps headers by default",
			argv:        []string{"config"},
			wantHeaders: true,
			wantBodies:  true,
		},
		{
			name:          "build summaries retain target labels when quiet",
			argv:          []string{"--quiet-targets", "build"},
			wantSummaries: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
				if stdout != nil || stderr != nil {
					return commandResult{target: stack, exitCode: 1, stderr: "expected buffered output"}
				}
				switch strings.Join(args, " ") {
				case "config":
					return commandResult{target: stack, stdout: fmt.Sprintf("config for %s", stack.Label)}
				case "build", "--progress=json build":
					return commandResult{target: stack, stdout: "noisy build output"}
				default:
					return commandResult{target: stack, exitCode: 1, stderr: "unexpected args"}
				}
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := runCLI(context.Background(), &stdout, &stderr, test.argv, runner)
			if code != 0 {
				t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
			}

			output := stdout.String()
			if test.wantHeaders {
				if !strings.Contains(output, "[.]\n") || !strings.Contains(output, "[api]\n") {
					t.Fatalf("expected section headers, got %q", output)
				}
			} else if strings.Contains(output, "[.]\n") || strings.Contains(output, "[api]\n") {
				t.Fatalf("expected no section headers, got %q", output)
			}
			if test.wantBodies && (!strings.Contains(output, "config for .") || !strings.Contains(output, "config for api")) {
				t.Fatalf("expected both config bodies, got %q", output)
			}
			if test.wantSummaries && (!strings.Contains(output, "[.] build complete\n") || !strings.Contains(output, "[api] build complete\n")) {
				t.Fatalf("expected labeled build summaries, got %q", output)
			}
		})
	}
}

func TestRunCLIReportsConfigQuietValidationSummary(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "config -q" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout != nil || stderr != nil {
			t.Fatal("expected buffered config output")
		}
		if stack.Label == "api" {
			return commandResult{target: stack, stderr: "invalid network", exitCode: 1, err: fmt.Errorf("invalid network")}
		}
		return commandResult{target: stack}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"config", "-q"}, runner)

	if code != 1 {
		t.Fatalf("expected failure exit code, got %d", code)
	}
	output := stdout.String()
	if !strings.Contains(output, ".\n") || !strings.Contains(output, "api (exit 1)\n") {
		t.Fatalf("expected plain validation summary, got %q", output)
	}
	if strings.Contains(output, "✔") || strings.Contains(output, "✘") {
		t.Fatalf("expected plain summary for non-TTY stdout, got %q", output)
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (invalid network)") {
		t.Fatalf("expected failure summary, got %q", stderr.String())
	}
}

func TestConfigValidationLine(t *testing.T) {
	tests := []struct {
		name      string
		result    commandResult
		decorated bool
		want      string
	}{
		{name: "plain pass", result: commandResult{target: target{Label: "api"}}, want: "api"},
		{name: "plain fail", result: commandResult{target: target{Label: "api"}, exitCode: 1}, want: "api (exit 1)"},
		{name: "decorated pass", result: commandResult{target: target{Label: "api"}}, decorated: true, want: "✔ api"},
		{name: "decorated fail", result: commandResult{target: target{Label: "api"}, exitCode: 3}, decorated: true, want: "✘ api (exit 3)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := configValidationLine(test.result, test.decorated); got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}

func TestConfigRequestsQuiet(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short flag", args: []string{"config", "-q"}, want: true},
		{name: "long flag", args: []string{"config", "--quiet"}, want: true},
		{name: "flag after other args", args: []string{"config", "--services", "-q"}, want: true},
		{name: "no quiet flag", args: []string{"config"}, want: false},
		{name: "quiet after separator ignored", args: []string{"config", "--", "-q"}, want: false},
		{name: "other command", args: []string{"up", "-d"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, index := composeCommand(test.args)
			if got := configRequestsQuiet(test.args, index); got != test.want {
				t.Fatalf("expected %t, got %t for %v", test.want, got, test.args)
			}
		})
	}
}

func TestRunCLISummarizesPullOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json pull" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout != nil || stderr != nil {
			t.Fatal("expected buffered pull output")
		}
		return commandResult{target: stack, stdout: "pull output that should be suppressed"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"pull"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "pull output that should be suppressed") {
		t.Fatalf("expected pull chatter to be suppressed, got %q", output)
	}
	if !strings.Contains(output, "[.] pull complete\n") {
		t.Fatalf("expected root pull summary, got %q", output)
	}
	if !strings.Contains(output, "[api] pull complete\n") {
		t.Fatalf("expected api pull summary, got %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunCLISuppressesSuccessfulParallelBuildOutput(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--progress=json build" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout != nil || stderr != nil {
			t.Fatal("expected buffered build output")
		}
		return commandResult{target: stack, stdout: "noisy buildkit output"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"build"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "noisy buildkit output") {
		t.Fatalf("expected build chatter to be suppressed, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[.] build complete\n") || !strings.Contains(stdout.String(), "[api] build complete\n") {
		t.Fatalf("expected per-target build summaries, got %q", stdout.String())
	}
}

func TestRunCLIUsesBuildBufferedPathWithLeadingComposeFlags(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "--ansi never --progress=json build" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout != nil || stderr != nil {
			t.Fatal("expected buffered build output")
		}
		return commandResult{target: stack, stdout: "suppressed buildkit output"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"--ansi", "never", "build"}, runner)
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "suppressed buildkit output") {
		t.Fatalf("expected build chatter to be suppressed, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[.] build complete\n") || !strings.Contains(stdout.String(), "[api] build complete\n") {
		t.Fatalf("expected per-target build summaries, got %q", stdout.String())
	}
}

func TestRunCLIKeepsBuildFailureDetailsAndSummary(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		switch stack.Label {
		case "api":
			return commandResult{target: stack, stderr: "denied", exitCode: 1, err: fmt.Errorf("denied")}
		default:
			return commandResult{target: stack, stdout: "suppressed success output"}
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"build"}, runner)
	if code != 1 {
		t.Fatalf("expected failure exit code, got %d", code)
	}
	if !strings.Contains(stdout.String(), "[.] build complete\n") {
		t.Fatalf("expected successful target summary, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[api]\ndenied\n") {
		t.Fatalf("expected failed target details, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "[api] build complete\n") {
		t.Fatalf("expected failed target to avoid success summary, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (denied)") {
		t.Fatalf("expected failure summary, got %q", stderr.String())
	}
}

func TestRunCLIBuildFailureSummaryUsesStdoutAndExitFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Run("stdout detail", func(t *testing.T) {
		runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
			if stack.Label == "api" {
				return commandResult{target: stack, stdout: "failed in stdout", exitCode: 2, err: fmt.Errorf("failed")}
			}
			return commandResult{target: stack, stdout: "ok"}
		}
		var out bytes.Buffer
		var errOut bytes.Buffer
		code := runCLI(context.Background(), &out, &errOut, []string{"build"}, runner)
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}
		if !strings.Contains(out.String(), "[api]\nfailed in stdout\n") {
			t.Fatalf("expected stdout-only failed target details, got %q", out.String())
		}
		if strings.Contains(out.String(), "[api] build complete\n") {
			t.Fatalf("expected failed target to avoid success summary, got %q", out.String())
		}
		if !strings.Contains(errOut.String(), "1 target(s) failed: api (failed in stdout)") {
			t.Fatalf("expected stdout-based summary, got %q", errOut.String())
		}
	})

	t.Run("exit fallback", func(t *testing.T) {
		runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
			if stack.Label == "api" {
				return commandResult{target: stack, exitCode: 7, err: fmt.Errorf("failed")}
			}
			return commandResult{target: stack, stdout: "ok"}
		}
		var out bytes.Buffer
		var errOut bytes.Buffer
		code := runCLI(context.Background(), &out, &errOut, []string{"build"}, runner)
		if code != 7 {
			t.Fatalf("expected exit 7, got %d", code)
		}
		if !strings.Contains(out.String(), "[api]\ncommand failed with exit code 7\n") {
			t.Fatalf("expected exit-code fallback in target output, got %q", out.String())
		}
		if !strings.Contains(errOut.String(), "1 target(s) failed: api (exit 7)") {
			t.Fatalf("expected exit-code summary, got %q", errOut.String())
		}
	})
}

func TestRunCLIKeepsPullFailureDetails(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if stack.Label == "api" {
			return commandResult{target: stack, stderr: "denied", exitCode: 1, err: fmt.Errorf("denied")}
		}
		return commandResult{target: stack, stdout: "pull output that should be suppressed"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"pull"}, runner)

	if code != 1 {
		t.Fatalf("expected failure exit code, got %d", code)
	}
	output := stdout.String()
	if !strings.Contains(output, "[.] pull complete\n") {
		t.Fatalf("expected successful target summary, got %q", output)
	}
	if !strings.Contains(output, "[api]\ndenied\n") {
		t.Fatalf("expected failed target details, got %q", output)
	}
	if !strings.Contains(stderr.String(), "1 target(s) failed: api (denied)") {
		t.Fatalf("expected failure summary, got %q", stderr.String())
	}
}

func TestRunCLIRejectsParallelEventsCommands(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		t.Fatal("runner should not be invoked for invalid parallel events command")
		return commandResult{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"events"}, runner)

	if code != 2 {
		t.Fatalf("expected usage error, got %d", code)
	}
	if !strings.Contains(stderr.String(), "docker compose events is live-streaming; rerun with --jobs 1") {
		t.Fatalf("expected live-streaming command error, got %q", stderr.String())
	}
}

func TestRunCLIAllowsSerialEventsPassthrough(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "events" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout == nil || stderr == nil {
			t.Fatal("expected passthrough writers for serial events")
		}
		if _, err := io.WriteString(stdout, "event output\n"); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
		return commandResult{target: stack, streamed: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"--jobs", "1", "events"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if strings.Count(output, "event output") != 2 {
		t.Fatalf("expected passthrough output from both targets, got %q", output)
	}
	if strings.Contains(output, "[.] event output") || strings.Contains(output, "[api] event output") {
		t.Fatalf("expected passthrough output without interleaving prefixes, got %q", output)
	}
}

func TestRunCLIRejectsParallelWatchCommands(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		t.Fatal("runner should not be invoked for invalid parallel watch command")
		return commandResult{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"watch"}, runner)

	if code != 2 {
		t.Fatalf("expected usage error, got %d", code)
	}
	if !strings.Contains(stderr.String(), "docker compose watch is long-running; rerun with --jobs 1") {
		t.Fatalf("expected long-running command error, got %q", stderr.String())
	}
}

func TestRunCLIBuffersParallelStatsWithNoStream(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var runnerErr error
	var runnerErrMu sync.Mutex
	recordRunnerErr := func(err error) {
		runnerErrMu.Lock()
		defer runnerErrMu.Unlock()
		if runnerErr == nil {
			runnerErr = err
		}
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		if strings.Join(args, " ") != "stats --no-stream" {
			recordRunnerErr(fmt.Errorf("unexpected args: %v", args))
		}
		if stdout != nil || stderr != nil {
			recordRunnerErr(fmt.Errorf("expected buffered stats output"))
		}
		return commandResult{target: stack, stdout: "CONTAINER ID NAME CPU % MEM %\nweb-1 0.00% 0.00%"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"stats"}, runner)
	if runnerErr != nil {
		t.Fatalf("runner assertion failed: %v", runnerErr)
	}
	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[.]\nCONTAINER ID NAME CPU % MEM %\nweb-1 0.00% 0.00%\n") {
		t.Fatalf("expected root stats section, got %q", output)
	}
	if !strings.Contains(output, "[api]\nCONTAINER ID NAME CPU % MEM %\nweb-1 0.00% 0.00%\n") {
		t.Fatalf("expected api stats section, got %q", output)
	}
}

func TestRunCLIPreservesStatsPassthroughForSerialAndSingleTarget(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	tests := []struct {
		name string
		argv []string
	}{
		{name: "serial stats", argv: []string{"--jobs", "1", "stats"}},
		{name: "single target stats", argv: []string{"--depth", "0", "stats"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
				if strings.Join(args, " ") != "stats" {
					t.Fatalf("unexpected args: %v", args)
				}
				if stdout == nil || stderr == nil {
					t.Fatal("expected passthrough writers for stats")
				}
				if _, err := io.WriteString(stdout, "stats line\n"); err != nil {
					t.Fatalf("write stdout: %v", err)
				}
				return commandResult{target: stack, streamed: true}
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := runCLI(context.Background(), &stdout, &stderr, tc.argv, runner)
			if code != 0 {
				t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
			}
			output := stdout.String()
			if strings.Contains(output, "[.] stats line") || strings.Contains(output, "[api] stats line") {
				t.Fatalf("expected passthrough stats output without target prefixes, got %q", output)
			}
			if tc.name == "serial stats" && strings.Count(output, "stats line") != 2 {
				t.Fatalf("expected output from both targets, got %q", output)
			}
			if tc.name == "single target stats" && strings.Count(output, "stats line") != 1 {
				t.Fatalf("expected output from one target, got %q", output)
			}
		})
	}
}

func TestRunCLIRejectsParallelInteractiveCommands(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, stdout io.Writer, stderr io.Writer) commandResult {
		t.Fatal("runner should not be invoked for invalid interactive parallel command")
		return commandResult{}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"exec", "app", "sh"}, runner)

	if code != 2 {
		t.Fatalf("expected usage error, got %d", code)
	}
	if !strings.Contains(stderr.String(), "docker compose exec is interactive; rerun with --jobs 1") {
		t.Fatalf("expected interactive command error, got %q", stderr.String())
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func valueAfterFlag(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestSelectPSRenderStylePicksPlainForNonTTY(t *testing.T) {
	if got := selectPSRenderStyle(&bytes.Buffer{}); got != psRenderStylePlain {
		t.Fatalf("expected plain style for non-TTY writer, got %v", got)
	}
}

func TestRenderPSTablePlainMatchesLegacyFormat(t *testing.T) {
	rows := []psRow{
		{Name: "root-web-1", Service: "web", Status: "running", Ports: "0.0.0.0:8080->80/tcp"},
		{Name: "api-web-1", Service: "web", Status: "running (healthy)", Ports: ""},
	}

	got := renderPSTable(rows, psRenderStylePlain)

	if strings.Count(got, "NAME") != 1 {
		t.Fatalf("expected single header, got %q", got)
	}
	if !strings.Contains(got, "root-web-1") || !strings.Contains(got, "api-web-1") {
		t.Fatalf("expected both rows, got %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("plain output should not contain ANSI escapes, got %q", got)
	}
}

func TestRenderPSTableStyledEmitsANSIEscapes(t *testing.T) {
	rows := []psRow{
		{Name: "root-web-1", Service: "web", Status: "running", Ports: "0.0.0.0:8080->80/tcp"},
		{Name: "api-web-1", Service: "web", Status: "running (healthy)", Ports: ""},
	}

	got := renderPSTable(rows, psRenderStyleStyled)

	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("styled output should contain ANSI escapes, got %q", got)
	}
	if !strings.Contains(got, ansiBold+"NAME") {
		t.Fatalf("expected bold NAME header, got %q", got)
	}
	if !strings.Contains(got, "─") {
		t.Fatalf("expected separator line, got %q", got)
	}
	if !strings.Contains(got, ansiReset) {
		t.Fatalf("expected reset escapes, got %q", got)
	}
}

func TestClassifyPSStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   psStatusKind
	}{
		{name: "bare running", status: "running", want: psStatusRunning},
		{name: "running prefix", status: "running (healthy)", want: psStatusHealthy},
		{name: "healthy paren", status: "Up 5 minutes (healthy)", want: psStatusHealthy},
		{name: "unhealthy", status: "Up 5 minutes (unhealthy)", want: psStatusUnhealthy},
		{name: "starting", status: "health: starting", want: psStatusStarting},
		{name: "paused", status: "paused", want: psStatusPaused},
		{name: "exited", status: "exited (0)", want: psStatusExited},
		{name: "exit code", status: "exit 137", want: psStatusExited},
		{name: "restarting", status: "restarting", want: psStatusRestarting},
		{name: "created", status: "created", want: psStatusCreated},
		{name: "dead", status: "dead", want: psStatusDead},
		{name: "unknown", status: "weird-state", want: psStatusUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyPSStatus(test.status); got != test.want {
				t.Fatalf("expected %v, got %v for %q", test.want, got, test.status)
			}
		})
	}
}

func TestStatusDecorationMapsKindsToColors(t *testing.T) {
	tests := []struct {
		kind       psStatusKind
		wantColor  string
		wantBullet string
	}{
		{kind: psStatusRunning, wantColor: ansiBlue, wantBullet: "●"},
		{kind: psStatusHealthy, wantColor: ansiGreen, wantBullet: "●"},
		{kind: psStatusUnhealthy, wantColor: ansiRed, wantBullet: "✖"},
		{kind: psStatusStarting, wantColor: ansiYellow, wantBullet: "◐"},
		{kind: psStatusExited, wantColor: ansiGray, wantBullet: "○"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("kind-%d", test.kind), func(t *testing.T) {
			deco := statusDecoration(test.kind)
			if deco.color != test.wantColor {
				t.Fatalf("expected color %q, got %q", test.wantColor, deco.color)
			}
			if deco.bullet != test.wantBullet {
				t.Fatalf("expected bullet %q, got %q", test.wantBullet, deco.bullet)
			}
		})
	}
}

func TestRenderPSTableStyledAlignsColumns(t *testing.T) {
	rows := []psRow{
		{Name: "short", Service: "svc", Status: "running", Ports: ""},
		{Name: "much-longer-container-name", Service: "service", Status: "running (healthy)", Ports: "0.0.0.0:8080->80/tcp"},
	}

	renderPSTable(rows, psRenderStyleStyled)
}

func TestSupportsJSONProgress(t *testing.T) {
	withProgress := []string{"build", "commit", "cp", "create", "down", "export", "kill", "pause", "pull", "publish", "push", "restart", "rm", "run", "scale", "start", "stop", "unpause", "up"}
	for _, command := range withProgress {
		if !supportsJSONProgress(command) {
			t.Fatalf("expected %s to support json progress", command)
		}
	}
	withoutProgress := []string{"attach", "config", "events", "exec", "images", "logs", "ls", "ps", "stats", "top", "version", "volumes", "watch"}
	for _, command := range withoutProgress {
		if supportsJSONProgress(command) {
			t.Fatalf("expected %s to not support json progress", command)
		}
	}
}

func TestUserSpecifiedProgress(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no progress flag", args: []string{"up", "-d"}, want: false},
		{name: "separate progress flag", args: []string{"up", "-d", "--progress", "tty"}, want: true},
		{name: "equals progress flag", args: []string{"up", "--progress=plain"}, want: true},
		{name: "leading global progress flag", args: []string{"--progress", "tty", "up", "-d"}, want: true},
		{name: "other flag with value", args: []string{"up", "--ansi", "never", "-d"}, want: false},
		{name: "leading global flags without progress", args: []string{"--ansi", "never", "up", "-d"}, want: false},
		{name: "progress on pull", args: []string{"pull", "--progress", "tty"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := userSpecifiedProgress(test.args); got != test.want {
				t.Fatalf("expected %t, got %t for %v", test.want, got, test.args)
			}
		})
	}
}

func TestInjectJSONProgress(t *testing.T) {
	tests := []struct {
		name string
		args []string
		mode outputMode
		want string
	}{
		{name: "buffered up", args: []string{"up", "-d"}, mode: outputModeBuffered, want: "--progress=json up -d"},
		{name: "interleaved up", args: []string{"up"}, mode: outputModeInterleaved, want: "--progress=json up"},
		{name: "passthrough up", args: []string{"up"}, mode: outputModePassthrough, want: "up"},
		{name: "user progress respected", args: []string{"up", "--progress", "tty"}, mode: outputModeBuffered, want: "up --progress tty"},
		{name: "pull", args: []string{"pull"}, mode: outputModeBuffered, want: "--progress=json pull"},
		{name: "unsupported command", args: []string{"logs", "--tail", "5"}, mode: outputModeBuffered, want: "logs --tail 5"},
		{name: "leading flags", args: []string{"--ansi", "never", "build"}, mode: outputModeBuffered, want: "--ansi never --progress=json build"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := injectJSONProgress(test.args, test.mode)
			if strings.Join(got, " ") != test.want {
				t.Fatalf("expected %q, got %q", test.want, strings.Join(got, " "))
			}
		})
	}
}

func TestInjectStatsNoStream(t *testing.T) {
	tests := []struct {
		name string
		args []string
		mode outputMode
		want string
	}{
		{name: "buffered stats", args: []string{"stats"}, mode: outputModeBuffered, want: "stats --no-stream"},
		{name: "buffered stats with existing no-stream", args: []string{"stats", "--no-stream"}, mode: outputModeBuffered, want: "stats --no-stream"},
		{name: "buffered stats with stream flag", args: []string{"stats", "--stream"}, mode: outputModeBuffered, want: "stats --stream"},
		{name: "buffered stats with equals no-stream", args: []string{"stats", "--no-stream=true"}, mode: outputModeBuffered, want: "stats --no-stream=true"},
		{name: "passthrough stats", args: []string{"stats"}, mode: outputModePassthrough, want: "stats"},
		{name: "leading flags stats", args: []string{"--ansi", "never", "stats"}, mode: outputModeBuffered, want: "--ansi never stats --no-stream"},
		{name: "non-stats command", args: []string{"up", "-d"}, mode: outputModeBuffered, want: "up -d"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := injectStatsNoStream(test.args, test.mode)
			if strings.Join(got, " ") != test.want {
				t.Fatalf("expected %q, got %q", test.want, strings.Join(got, " "))
			}
		})
	}
}

func TestParseProgressLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want progressEvent
		ok   bool
	}{
		{
			name: "full event",
			line: `{"id":"Container example-web-1","parent_id":"","status":"Working","text":"Creating","details":"","current":0,"total":0,"percent":0}`,
			want: progressEvent{ID: "Container example-web-1", Status: "Working", Text: "Creating"},
			ok:   true,
		},
		{name: "id and text", line: `{"id":"x","text":"y"}`, want: progressEvent{ID: "x", Text: "y"}, ok: true},
		{name: "id only", line: `{"id":"x"}`, want: progressEvent{ID: "x"}, ok: true},
		{name: "text only", line: `{"text":"y"}`, want: progressEvent{Text: "y"}, ok: true},
		{name: "status only", line: `{"status":"Done"}`, ok: false},
		{name: "not json", line: "Container fake-svc Starting", ok: false},
		{name: "empty", line: "", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseProgressLine(test.line)
			if ok != test.ok {
				t.Fatalf("expected ok=%t, got %t for %q", test.ok, ok, test.line)
			}
			if ok && got != test.want {
				t.Fatalf("expected %+v, got %+v", test.want, got)
			}
		})
	}
}

func TestTransformProgressLinesRendersComposeLogRecords(t *testing.T) {
	warning := `{"level":"warning","msg":"The \"version\" field is obsolete and will be ignored. It's recommended to remove it to avoid potential confusion","time":"2026-08-15T10:00:00Z"}`
	error := `{"error":true,"message":"invalid interpolation format for services.price-stalker.environment.[]: \"${PRICE_STALKER_IMAGE}\"\nYou may need to escape any $ with another $."}`

	want := "Warning: The \"version\" field is obsolete and will be ignored. It's recommended to remove it to avoid potential confusion\nError: invalid interpolation format for services.price-stalker.environment.[]: \"${PRICE_STALKER_IMAGE}\"\nYou may need to escape any $ with another $."
	if got := transformProgressLines(warning + "\n" + error); got != want {
		t.Fatalf("expected normalized compose diagnostics %q, got %q", want, got)
	}
}

func TestTransformProgressLinesLeavesUnknownOutputUnchanged(t *testing.T) {
	input := "{\"message\":\"not a compose error\"}\n{malformed}\nplain text"
	if got := transformProgressLines(input); got != input {
		t.Fatalf("expected unknown output unchanged, got %q", got)
	}
}

func TestLiveStatusWriterParsesProgressEvents(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoard(&dest, []target{{Label: "."}, {Label: "api"}})
	board.start()

	writer := &liveStatusWriter{board: board, target: target{Label: "api"}}
	if _, err := io.WriteString(writer, `{"id":"Container api-web-1","status":"Working","text":"Creating"}`+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()
	if got := board.statuses["api"]; got != "api-web-1 Creating" {
		t.Fatalf("expected Creating status, got %q", got)
	}

	if _, err := io.WriteString(writer, `{"id":"Container api-web-1","status":"Warning","text":"healthcheck failed"}`+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()
	if got := board.statuses["api"]; got != "api-web-1 Warning: healthcheck failed (Container api-web-1)" {
		t.Fatalf("expected warning status, got %q", got)
	}

	if _, err := io.WriteString(writer, "Container api-web-1 Started\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()
	if got := board.statuses["api"]; got != "api-web-1 Started" {
		t.Fatalf("expected latest status, got %q", got)
	}
}

func TestLiveStatusBoardRendersOneStatusRowPerTarget(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoard(&dest, []target{{Label: "api"}, {Label: "web"}})
	board.start()

	api := &liveStatusWriter{board: board, target: target{Label: "api"}}
	web := &liveStatusWriter{board: board, target: target{Label: "web"}}

	writeEvent := func(w *liveStatusWriter, line string) {
		t.Helper()
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writeEvent(api, `{"id":"Image nginx","status":"Working","text":"Pulling","percent":40}`)
	writeEvent(api, `{"id":"Container api-web-1","status":"Working","text":"Creating"}`)
	writeEvent(web, `{"id":"Image busybox","status":"Working","text":"Pulling","current":50,"total":200}`)
	writeEvent(web, `{"id":"Container web-svc-1","status":"Working","text":"Starting"}`)
	writeEvent(web, `{"id":"Container web-svc-1","status":"Warning","text":"restarting"}`)
	writeEvent(web, `{"id":"layer-ab12","parent_id":"Image busybox","status":"Working","text":"Downloading"}`)
	// A later top-level event replaces the target's earlier activity.
	writeEvent(api, `{"id":"Image nginx","status":"Working","text":"Extracting","percent":75}`)

	if got := board.statuses["api"]; got != "nginx Extracting (75%)" {
		t.Fatalf("expected latest api status, got %q", got)
	}
	if got := board.statuses["web"]; got != "web-svc-1 Warning: restarting (Container web-svc-1)" {
		t.Fatalf("expected latest web status, got %q", got)
	}

	wantFrame := "" +
		"\r\x1b[2K[api] nginx Extracting (75%)\n" +
		"\r\x1b[2K[web] web-svc-1 Warning: restarting (Container web-svc-1)\n"
	if !strings.HasSuffix(dest.String(), "\x1b[2A"+wantFrame) {
		t.Fatalf("unexpected final frame:\n%q", dest.String())
	}

	board.finish()
	wantFinish := "\x1b[2A" + strings.Repeat("\r\x1b[2K\n", 2) + "\x1b[2A"
	if !strings.HasSuffix(dest.String(), wantFinish) {
		t.Fatalf("finish did not clear the rendered lines:\n%q", dest.String())
	}
}

func TestLiveStatusBoardUpdatesStatusFromPlainComposeLine(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoard(&dest, []target{{Label: "api"}})
	board.start()

	writer := &liveStatusWriter{board: board, target: target{Label: "api"}}
	if _, err := io.WriteString(writer, "Container api-web-1 Started\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()

	if got := board.statuses["api"]; got != "api-web-1 Started" {
		t.Fatalf("expected latest status, got %q", got)
	}
	if !strings.Contains(dest.String(), "[api] api-web-1 Started") {
		t.Fatalf("expected status row rendered, got %q", dest.String())
	}
}

func TestLiveStatusBoardUpdatesCompletionStatePerTarget(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoard(&dest, []target{{Label: "api"}, {Label: "web"}})
	board.start()

	board.complete("api", commandResult{exitCode: 0})
	if got := board.statuses["api"]; got != "succeeded" {
		t.Fatalf("expected successful completion state, got %q", got)
	}
	if got := board.statuses["web"]; got != "waiting" {
		t.Fatalf("expected unfinished target to remain waiting, got %q", got)
	}

	frame := dest.String()[strings.LastIndex(dest.String(), "\x1b[2A")+len("\x1b[2A"):]
	if !strings.Contains(frame, "[api] succeeded") || !strings.Contains(frame, "[web] waiting") {
		t.Fatalf("expected completion update to render independently, got %q", frame)
	}

	board.complete("web", commandResult{exitCode: 1})
	if got := board.statuses["web"]; got != "failed" {
		t.Fatalf("expected failed completion state, got %q", got)
	}
}

func TestWriteLiveReportDeduplicatesWarningsPerTargetIncludingSuccesses(t *testing.T) {
	warning := `{"level":"warning","msg":"The \"version\" field is obsolete and will be ignored. It's recommended to remove it to avoid potential confusion"}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	writeLiveReport(&stdout, &stderr, []commandResult{
		{target: target{Label: "api"}, stderr: warning, stdout: warning},
		{target: target{Label: "web"}, stderr: warning, stdout: warning, exitCode: 3},
	})

	if got, want := stdout.String(), "1 target succeeded, 1 target failed\n"; got != want {
		t.Fatalf("expected aggregate count %q, got %q", want, got)
	}
	if strings.Contains(stdout.String(), "Warning:") {
		t.Fatalf("expected diagnostics on stderr, got %q", stdout.String())
	}
	if got := strings.Count(stderr.String(), "Warning: The \"version\" field is obsolete"); got != 2 {
		t.Fatalf("expected one warning per target, got %d in %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Warnings:\n[api]") || !strings.Contains(stderr.String(), "[web]\n") {
		t.Fatalf("expected warnings grouped by target, got %q", stderr.String())
	}
}

func TestWriteLiveReportPreservesMultilineComposeError(t *testing.T) {
	message := "error while interpolating services.db.environment.JWT_SECRET: required variable JWT_SECRET is missing a value: JWT_SECRET is required\nerror while interpolating services.db.environment.POSTGRES_PASSWORD: required variable POSTGRES_PASSWORD is missing a value: POSTGRES_PASSWORD is required"
	errorEnvelope := fmt.Sprintf(`{"error":true,"message":%q}`, message)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	writeLiveReport(&stdout, &stderr, []commandResult{
		{target: target{Label: "api"}, stderr: errorEnvelope, exitCode: 1},
	})

	want := "Failed:\n[api]\nError: " + message + "\n"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("expected exact multiline compose error detail %q, got %q", want, stderr.String())
	}
}

func TestWriteLiveReportUsesExitCodeFallback(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	writeLiveReport(&stdout, &stderr, []commandResult{
		{target: target{Label: "api"}, exitCode: 7},
	})

	if !strings.Contains(stderr.String(), "Failed:\n[api]\nexit 7\n") {
		t.Fatalf("expected exit-code failure fallback, got %q", stderr.String())
	}
}

func TestWriteLiveReportOmitsPerTargetSummaries(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	writeLiveReport(&stdout, &stderr, []commandResult{
		{target: target{Label: "."}, stdout: "plain success output"},
		{target: target{Label: "api"}, stderr: "plain stderr line 1\nplain stderr line 2", stdout: "plain stdout detail", exitCode: 7},
	})

	if got, want := stdout.String(), "1 target succeeded, 1 target failed\n"; got != want {
		t.Fatalf("expected live aggregate, got %q", got)
	}
	for _, summary := range []string{"up complete", "pull complete", "build complete"} {
		if strings.Contains(stdout.String(), summary) {
			t.Fatalf("expected no per-target %q summary, got %q", summary, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "Failed:\n[api]\nplain stderr line 1\nplain stderr line 2\nplain stdout detail\n") {
		t.Fatalf("expected readable plain failure detail, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "target(s) failed") {
		t.Fatalf("expected live report to omit old failure summary, got %q", stderr.String())
	}
}

func TestProgressPercent(t *testing.T) {
	tests := []struct {
		name  string
		event progressEvent
		want  int
	}{
		{name: "explicit percent wins", event: progressEvent{Percent: 40, Current: 1, Total: 10}, want: 40},
		{name: "derived from current and total", event: progressEvent{Current: 50, Total: 200}, want: 25},
		{name: "truncates", event: progressEvent{Current: 1, Total: 3}, want: 33},
		{name: "zero without data", event: progressEvent{}, want: 0},
		{name: "current without total", event: progressEvent{Current: 5}, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := progressPercent(test.event); got != test.want {
				t.Fatalf("expected %d, got %d", test.want, got)
			}
		})
	}
}

func TestLiveStatusBoardTruncatesRowsToTerminalWidth(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoardWithWidth(&dest, []target{{Label: "api"}, {Label: "very-long-target"}}, 24)
	board.start()
	board.update("api", "a status that is much longer than the terminal width")
	board.update("very-long-target", "another status that must not wrap")

	if board.renderedLines != 2 {
		t.Fatalf("expected one rendered line per target, got %d", board.renderedLines)
	}
	frameStart := strings.LastIndex(dest.String(), "\x1b[2A")
	if frameStart < 0 {
		t.Fatalf("expected a rendered update frame, got %q", dest.String())
	}
	frame := dest.String()[frameStart+len("\x1b[2A"):]
	rows := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")
	if len(rows) != 2 {
		t.Fatalf("expected two rows in final frame, got %q", frame)
	}
	for _, row := range rows {
		row = strings.TrimPrefix(row, "\r\x1b[2K")
		if visibleWidth(row) > 24 {
			t.Fatalf("row exceeds terminal width: %q", row)
		}
	}
}

func TestLiveStatusBoardHumanizesLongImageDigest(t *testing.T) {
	var dest bytes.Buffer
	board := newLiveStatusBoardWithWidth(&dest, []target{{Label: "api"}}, 100)
	board.start()

	digest := strings.Repeat("a", 64)
	writer := &liveStatusWriter{board: board, target: target{Label: "api"}}
	line := fmt.Sprintf(`{"id":"Image ghcr.io/acme/app:latest@sha256:%s","status":"Working","text":"Pulling","percent":40}`, digest)
	if _, err := io.WriteString(writer, line+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()

	want := "ghcr.io/acme/app:latest Pulling (40%)"
	if got := board.statuses["api"]; got != want {
		t.Fatalf("expected humanized image activity %q, got %q", want, got)
	}
	if strings.Contains(dest.String(), "sha256:") {
		t.Fatalf("expected digest omitted from live board, got %q", dest.String())
	}
}

func TestLinePrefixWriterRendersProgressEvents(t *testing.T) {
	var dest bytes.Buffer
	var mu sync.Mutex
	writer := &linePrefixWriter{dest: &dest, prefix: "[api] ", mu: &mu, atLineStart: true}

	input := `{"id":"Container api-web-1","status":"Working","text":"Creating"}` + "\n" +
		"plain line\n" +
		`{"id":"Container api-web-1","status":"Error","text":"boom"}` + "\n"
	if _, err := io.WriteString(writer, input); err != nil {
		t.Fatalf("write: %v", err)
	}
	writer.finishStreamingOutput()

	want := "[api] Container api-web-1: Creating\n[api] plain line\n[api] Error: Container api-web-1 (boom)\n"
	if got := dest.String(); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestLinePrefixWriterRendersComposeLogRecords(t *testing.T) {
	var dest bytes.Buffer
	var mu sync.Mutex
	writer := &linePrefixWriter{dest: &dest, prefix: "[api] ", mu: &mu, atLineStart: true}

	input := `{"level":"warning","msg":"The \"version\" field is obsolete and will be ignored. It's recommended to remove it to avoid potential confusion","time":"2026-08-15T10:00:00Z"}` + "\n" +
		`{"error":true,"message":"invalid interpolation format for services.price-stalker.environment.[]: \"${PRICE_STALKER_IMAGE}\"\nYou may need to escape any $ with another $."}` + "\n"
	if _, err := io.WriteString(writer, input); err != nil {
		t.Fatalf("write: %v", err)
	}

	want := "[api] Warning: The \"version\" field is obsolete and will be ignored. It's recommended to remove it to avoid potential confusion\n[api] Error: invalid interpolation format for services.price-stalker.environment.[]: \"${PRICE_STALKER_IMAGE}\"\n[api] You may need to escape any $ with another $.\n"
	if got := dest.String(); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestTargetOutputTransformsProgressLinesInStderr(t *testing.T) {
	result := commandResult{
		target:   target{Label: "api"},
		stdout:   `{"id":"x","text":"stdout json kept"}`,
		stderr:   "{\"id\":\"Container api-web-1\",\"status\":\"Error\",\"text\":\"boom\"}\nplain error",
		exitCode: 1,
	}
	got := targetOutput(result, false)
	if !strings.Contains(got, `{"id":"x","text":"stdout json kept"}`) {
		t.Fatalf("expected stdout left verbatim, got %q", got)
	}
	if !strings.Contains(got, "Error: Container api-web-1 (boom)\nplain error") {
		t.Fatalf("expected stderr progress lines re-rendered, got %q", got)
	}
}

func TestWriteFailureSummaryTransformsProgressLines(t *testing.T) {
	var stderr bytes.Buffer
	writeFailureSummary(&stderr, []commandResult{
		{target: target{Label: "api"}, stderr: "{\"id\":\"Container api-web-1\",\"status\":\"Error\",\"text\":\"boom\"}\nmore", exitCode: 1},
	})
	if got := stderr.String(); !strings.Contains(got, "api (Error: Container api-web-1 (boom))") {
		t.Fatalf("expected transformed failure summary, got %q", got)
	}
}

func TestWriteFailureSummaryRendersComposeErrorRecord(t *testing.T) {
	var stderr bytes.Buffer
	writeFailureSummary(&stderr, []commandResult{
		{target: target{Label: "api"}, stderr: `{"error":true,"message":"invalid interpolation format for services.price-stalker.environment.[]: \"${PRICE_STALKER_IMAGE}\"\nYou may need to escape any $ with another $."}`, exitCode: 1},
	})
	if got := stderr.String(); !strings.Contains(got, "api (Error: invalid interpolation format for services.price-stalker.environment.[]: \"${PRICE_STALKER_IMAGE}\")") {
		t.Fatalf("expected compose error in failure summary, got %q", got)
	}
}

func TestDiscoverTargetsSkipsUnreadableSubdirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWriteFile(t, filepath.Join(locked, "compose.yaml"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o755) // let TempDir cleanup succeed
	})

	// depth 1 exercises the canonicalComposeFile stat path (Path B):
	// the walker never ReadDirs `locked`, but it does stat files inside it.
	targets, err := discoverTargets(root, 1)
	if err != nil {
		t.Fatalf("expected unreadable subdir to be skipped at depth 1, got error: %v", err)
	}

	labels := make([]string, 0, len(targets))
	for _, tg := range targets {
		labels = append(labels, tg.Label)
	}
	sort.Strings(labels)
	if strings.Join(labels, ",") != ".,api" {
		t.Fatalf("expected targets [., api], got %v", labels)
	}

	// depth 2 exercises the collectDirectories ReadDir path (Path A).
	targets, err = discoverTargets(root, 2)
	if err != nil {
		t.Fatalf("expected unreadable subdir to be skipped at depth 2, got error: %v", err)
	}
	labels = labels[:0]
	for _, tg := range targets {
		labels = append(labels, tg.Label)
	}
	sort.Strings(labels)
	if strings.Join(labels, ",") != ".,api" {
		t.Fatalf("expected targets [., api] at depth 2, got %v", labels)
	}
}

func TestRunCLIInterruptedRunSkipsFailureSummary(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := func(ctx context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		cancel()
		<-ctx.Done()
		// exitCode -1 = what a signal-killed docker produces via ExitCode().
		return commandResult{target: stack, exitCode: -1, err: ctx.Err()}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(ctx, &stdout, &stderr, []string{"up", "-d"}, runner)

	if code != 130 {
		t.Fatalf("expected exit code 130, got %d", code)
	}
	if strings.Contains(stderr.String(), "target(s) failed") {
		t.Fatalf("expected no failure summary after interrupt, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("expected interruption notice, got %q", stderr.String())
	}
}

func TestExecComposeClampsSignaledExitCode(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	targetDir := filepath.Join(root, "stack")
	composeFile := filepath.Join(targetDir, "compose.yaml")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", binDir, err)
	}
	mustWriteFile(t, composeFile)

	scriptPath := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\nkill -TERM $$\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", scriptPath, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := execCompose(context.Background(), target{Dir: targetDir, File: composeFile, Label: "."}, []string{"up", "-d"}, &stdout, &stderr)

	if result.exitCode != 1 {
		t.Fatalf("expected clamped exit code 1 for signaled process, got %d", result.exitCode)
	}
}

func TestRunCLIWarnsOnUnrecognizedComposeFlags(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		return commandResult{target: stack, stdout: "ok", exitCode: 0}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"--no-such-flag", "ps"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "unrecognized docker compose flags") {
		t.Fatalf("expected warning, got %q", stderr.String())
	}
}

func TestComposeCommandArgsForGlobalCommandOmitProjectFlags(t *testing.T) {
	got := composeCommandArgs(target{}, []string{"ls"})
	if strings.Join(got, " ") != "compose ls" {
		t.Fatalf("expected compose ls without project flags, got %v", got)
	}
}

func TestExecComposeRunsGlobalCommandWithoutProjectFlags(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", binDir, err)
	}

	scriptPath := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", scriptPath, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := execCompose(context.Background(), target{}, []string{"ls"}, &stdout, &stderr)

	if result.exitCode != 0 {
		t.Fatalf("expected success, got %+v", result)
	}
	lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
	if strings.Join(lines, " ") != "compose ls" {
		t.Fatalf("expected global command args, got %q", result.stdout)
	}
}

func TestRunCLIRunsGlobalComposeCommandOnce(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "compose ls runs once", argv: []string{"ls"}, want: "ls"},
		{name: "compose version after separator runs once", argv: []string{"--", "version"}, want: "version"},
		{name: "compose version after mdc flag runs once", argv: []string{"--depth", "0", "version"}, want: "version"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
				calls++
				if got := strings.Join(args, " "); got != test.want {
					t.Fatalf("expected args %q, got %q", test.want, got)
				}
				if stack.Dir != "" || stack.File != "" {
					t.Fatalf("expected empty target for global command, got %+v", stack)
				}
				return commandResult{stdout: "global output"}
			}

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := runCLI(context.Background(), &stdout, &stderr, test.argv, runner)
			if code != 0 {
				t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
			}
			if calls != 1 {
				t.Fatalf("expected exactly 1 invocation, got %d", calls)
			}
			if !strings.Contains(stdout.String(), "global output") {
				t.Fatalf("expected passthrough output, got %q", stdout.String())
			}
		})
	}
}

func TestRunCLIPropagatesGlobalComposeFailure(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		if strings.Join(args, " ") != "ls" {
			t.Fatalf("unexpected args: %v", args)
		}
		return commandResult{stderr: "no such project", exitCode: 7, err: fmt.Errorf("no such project")}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"ls"}, runner)
	if code != 7 {
		t.Fatalf("expected exit code 7, got %d", code)
	}
	if !strings.Contains(stderr.String(), "no such project") {
		t.Fatalf("expected failure output on stderr, got %q", stderr.String())
	}
}

func TestRunPSReportsExplicitJSONFailureWithoutRerun(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var calls atomic.Int32
	runner := func(_ context.Context, stack target, args []string, _ io.Writer, _ io.Writer) commandResult {
		calls.Add(1)
		return commandResult{target: stack, stderr: "boom", exitCode: 5, err: fmt.Errorf("boom")}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"ps", "--format", "json"}, runner)

	if code != 5 {
		t.Fatalf("expected exit code 5, got %d", code)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", calls.Load())
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("expected failure detail, got %q", stderr.String())
	}
}

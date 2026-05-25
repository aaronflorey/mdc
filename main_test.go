package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
		{name: "parallel up interleaves", args: []string{"up", "-d"}, jobs: 0, targets: 2, wantMode: outputModeInterleaved},
		{name: "serial up passes through", args: []string{"up", "-d"}, jobs: 1, targets: 2, wantMode: outputModePassthrough},
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
		if strings.Join(args, " ") != "up -d" {
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
	if !strings.Contains(stdout.String(), "[.]\nok") {
		t.Fatalf("expected successful target output, got %q", stdout.String())
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
		if strings.Join(args, " ") != "up -d" {
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

func TestRunCLIInterleavesLiveCommandOutput(t *testing.T) {
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
		if strings.Join(args, " ") != "up -d" {
			t.Fatalf("unexpected args: %v", args)
		}
		if stdout == nil || stderr == nil {
			t.Fatal("expected live writers for interleaved command")
		}
		if _, err := io.WriteString(stdout, "started\n"); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
		return commandResult{target: stack, streamed: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(context.Background(), &stdout, &stderr, []string{"up", "-d"}, runner)

	if code != 0 {
		t.Fatalf("expected success, got %d with stderr %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[.] started\n") {
		t.Fatalf("expected root target output, got %q", output)
	}
	if !strings.Contains(output, "[api] started\n") {
		t.Fatalf("expected api target output, got %q", output)
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
				if strings.Join(args, " ") != "build" {
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
		if strings.Join(args, " ") != "pull" {
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
		if strings.Join(args, " ") != "build" {
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
		if strings.Join(args, " ") != "--ansi never build" {
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

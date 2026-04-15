package main

import (
	"bytes"
	"context"
	"fmt"
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

func TestComposeProjectNameIsStableAndUnique(t *testing.T) {
	first := composeProjectName(filepath.Join(string(os.PathSeparator), "tmp", "services", "app"))
	second := composeProjectName(filepath.Join(string(os.PathSeparator), "srv", "examples", "app"))
	repeated := composeProjectName(filepath.Join(string(os.PathSeparator), "tmp", "services", "app"))

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

func TestComposeCommandArgsIncludeProjectName(t *testing.T) {
	dirOne := filepath.Join(string(os.PathSeparator), "tmp", "services", "app")
	dirTwo := filepath.Join(string(os.PathSeparator), "srv", "examples", "app")

	argsOne := composeCommandArgs(target{Dir: dirOne, File: filepath.Join(dirOne, "compose.yaml")}, []string{"up", "-d"})
	argsTwo := composeCommandArgs(target{Dir: dirTwo, File: filepath.Join(dirTwo, "compose.yaml")}, []string{"up", "-d"})

	projectOne := valueAfterFlag(argsOne, "--project-name")
	projectTwo := valueAfterFlag(argsTwo, "--project-name")
	if projectOne == "" || projectTwo == "" {
		t.Fatalf("expected project names in args, got %v and %v", argsOne, argsTwo)
	}
	if projectOne == projectTwo {
		t.Fatalf("expected distinct project names, got %q", projectOne)
	}
	if got := strings.Join(argsOne[len(argsOne)-2:], " "); got != "up -d" {
		t.Fatalf("expected docker compose args at the end, got %v", argsOne)
	}
	if valueAfterFlag(argsOne, "-f") != filepath.Join(dirOne, "compose.yaml") {
		t.Fatalf("expected compose file in args, got %v", argsOne)
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

	runner := func(_ context.Context, stack target, args []string) commandResult {
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
	runner := func(ctx context.Context, stack target, args []string) commandResult {
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

	runner := func(_ context.Context, stack target, args []string) commandResult {
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
	runner := func(_ context.Context, stack target, args []string) commandResult {
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
	runner := func(_ context.Context, stack target, args []string) commandResult {
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

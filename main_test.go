package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

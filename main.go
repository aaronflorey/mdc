package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aaronflorey/multi-docker-compose/internal/version"
)

const (
	commandName  = "mdc"
	defaultDepth = 1
)

var composeFilenames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

type action int

const (
	actionRun action = iota
	actionHelp
	actionVersion
)

type options struct {
	depth        int
	jobs         int
	quietTargets bool
}

type target struct {
	Dir   string
	File  string
	Label string
}

type commandResult struct {
	target   target
	stdout   string
	stderr   string
	exitCode int
	err      error
}

type composeRunner func(context.Context, target, []string) commandResult

type psRow struct {
	Name    string
	Service string
	Status  string
	Ports   string
	key     string
}

func main() {
	code := runCLI(context.Background(), os.Stdout, os.Stderr, os.Args[1:], execCompose)
	os.Exit(code)
}

func runCLI(ctx context.Context, stdout io.Writer, stderr io.Writer, argv []string, runner composeRunner) int {
	opts, composeArgs, nextAction, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printUsage(stderr)
		return 2
	}

	switch nextAction {
	case actionHelp:
		printUsage(stdout)
		return 0
	case actionVersion:
		fmt.Fprintf(stdout, "%s %s\n", commandName, versionString())
		return 0
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return 1
	}

	targets, err := discoverTargets(workingDir, opts.depth)
	if err != nil {
		fmt.Fprintf(stderr, "discover compose files: %v\n", err)
		return 1
	}
	if len(targets) == 0 {
		fmt.Fprintln(stderr, "no compose files found in the current directory tree")
		return 1
	}

	if shouldMergePS(composeArgs) {
		return runPS(ctx, stdout, stderr, targets, opts, composeArgs, runner)
	}

	results := executeTargets(ctx, targets, composeArgs, opts.jobs, runner)
	writeStandardOutput(stdout, results, opts.quietTargets)

	if failures := failureResults(results); len(failures) > 0 {
		writeFailureSummary(stderr, failures)
		return failures[0].exitCode
	}

	return 0
}

func parseArgs(args []string) (options, []string, action, error) {
	opts := options{depth: defaultDepth}
	if len(args) == 0 {
		return opts, nil, actionHelp, nil
	}

	if len(args) == 1 {
		switch args[0] {
		case "help", "-h", "--help":
			return opts, nil, actionHelp, nil
		case "version", "--version":
			return opts, nil, actionVersion, nil
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			if i+1 >= len(args) {
				return opts, nil, actionRun, errors.New("expected docker compose arguments after --")
			}
			return opts, args[i+1:], actionRun, nil
		}

		switch {
		case arg == "--quiet-targets":
			opts.quietTargets = true
		case strings.HasPrefix(arg, "--depth="):
			depth, err := parsePositiveInt(strings.TrimPrefix(arg, "--depth="), "depth")
			if err != nil {
				return opts, nil, actionRun, err
			}
			opts.depth = depth
		case arg == "--depth":
			if i+1 >= len(args) {
				return opts, nil, actionRun, errors.New("missing value for --depth")
			}
			depth, err := parsePositiveInt(args[i+1], "depth")
			if err != nil {
				return opts, nil, actionRun, err
			}
			opts.depth = depth
			i++
		case strings.HasPrefix(arg, "--jobs="):
			jobs, err := parseNonNegativeInt(strings.TrimPrefix(arg, "--jobs="), "jobs")
			if err != nil {
				return opts, nil, actionRun, err
			}
			opts.jobs = jobs
		case arg == "--jobs":
			if i+1 >= len(args) {
				return opts, nil, actionRun, errors.New("missing value for --jobs")
			}
			jobs, err := parseNonNegativeInt(args[i+1], "jobs")
			if err != nil {
				return opts, nil, actionRun, err
			}
			opts.jobs = jobs
			i++
		default:
			return opts, args[i:], actionRun, nil
		}
	}

	return opts, nil, actionRun, errors.New("expected docker compose arguments")
}

func parsePositiveInt(raw string, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}

func parseNonNegativeInt(raw string, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, "%s runs docker compose across multiple compose projects in the current tree.\n\n", commandName)
	fmt.Fprintf(w, "Usage:\n  %s [mdc flags] <docker-compose args...>\n  %s --help\n  %s --version\n\n", commandName, commandName, commandName)
	fmt.Fprintln(w, "mdc flags:")
	fmt.Fprintf(w, "  --depth N          Discovery depth. 0 = current dir only, default %d\n", defaultDepth)
	fmt.Fprintln(w, "  --jobs N           Max concurrent docker compose commands. 0 = all targets")
	fmt.Fprintln(w, "  --quiet-targets    Suppress per-target section labels for non-merged output")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintf(w, "  %s ps\n", commandName)
	fmt.Fprintf(w, "  %s up -d\n", commandName)
	fmt.Fprintf(w, "  %s --depth 2 pull\n", commandName)
	fmt.Fprintf(w, "  %s --ansi never ps\n", commandName)
}

func versionString() string {
	parts := []string{version.Version}
	if version.Commit != "" && version.Commit != "none" {
		parts = append(parts, version.Commit)
	}
	if version.Date != "" && version.Date != "unknown" {
		parts = append(parts, version.Date)
	}
	return strings.Join(parts, " ")
}

func discoverTargets(root string, maxDepth int) ([]target, error) {
	dirs, err := collectDirectories(root, maxDepth)
	if err != nil {
		return nil, err
	}

	targets := make([]target, 0, len(dirs))
	for _, dir := range dirs {
		file, ok, err := canonicalComposeFile(dir)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return nil, err
		}

		label := "."
		if rel != "." {
			label = filepath.ToSlash(rel)
		}

		targets = append(targets, target{Dir: dir, File: file, Label: label})
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Label < targets[j].Label
	})

	return targets, nil
}

func collectDirectories(root string, maxDepth int) ([]string, error) {
	if maxDepth < 0 {
		return nil, errors.New("depth must be a non-negative integer")
	}

	type queuedDir struct {
		path  string
		depth int
	}

	queue := []queuedDir{{path: root, depth: 0}}
	dirs := []string{root}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		entries, err := os.ReadDir(current.path)
		if err != nil {
			return nil, fmt.Errorf("read directory %q: %w", current.path, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			child := filepath.Join(current.path, entry.Name())
			dirs = append(dirs, child)
			queue = append(queue, queuedDir{path: child, depth: current.depth + 1})
		}
	}

	sort.Strings(dirs)
	return dirs, nil
}

func canonicalComposeFile(dir string) (string, bool, error) {
	for _, name := range composeFilenames {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("stat compose file %q: %w", candidate, err)
		}
	}

	return "", false, nil
}

func execCompose(ctx context.Context, stack target, args []string) commandResult {
	commandArgs := composeCommandArgs(stack, args)

	cmd := exec.CommandContext(ctx, "docker", commandArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	return commandResult{
		target:   stack,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: exitCode,
		err:      err,
	}
}

func composeCommandArgs(stack target, args []string) []string {
	commandArgs := []string{
		"compose",
		"--project-name", composeProjectName(stack.Dir),
		"-f", stack.File,
		"--project-directory", stack.Dir,
	}
	commandArgs = append(commandArgs, args...)
	return commandArgs
}

func composeProjectName(dir string) string {
	cleanedDir := filepath.Clean(dir)
	if absoluteDir, err := filepath.Abs(cleanedDir); err == nil {
		cleanedDir = absoluteDir
	}

	base := sanitizeComposeProjectComponent(filepath.Base(cleanedDir))
	if base == "" {
		base = "root"
	}

	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(filepath.ToSlash(cleanedDir)))

	return fmt.Sprintf("mdc-%s-%x", base, hasher.Sum64())
}

func sanitizeComposeProjectComponent(value string) string {
	value = strings.ToLower(value)

	var builder strings.Builder
	lastSeparator := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastSeparator = false
		case builder.Len() > 0 && !lastSeparator:
			builder.WriteByte('-')
			lastSeparator = true
		}
	}

	return strings.Trim(builder.String(), "-")
}

func executeTargets(ctx context.Context, targets []target, args []string, jobs int, runner composeRunner) []commandResult {
	results := make([]commandResult, len(targets))
	limit := jobs
	if limit <= 0 || limit > len(targets) {
		limit = len(targets)
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i, stack := range targets {
		wg.Add(1)
		go func(index int, stack target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[index] = runner(ctx, stack, args)
		}(i, stack)
	}

	wg.Wait()
	return results
}

func shouldMergePS(args []string) bool {
	psIndex := psCommandIndex(args)
	if psIndex < 0 {
		return false
	}

	format, hasFormat := psFormat(args, psIndex)
	if !hasFormat {
		return true
	}

	return strings.EqualFold(format, "json")
}

func psCommandIndex(args []string) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if composeGlobalFlagTakesValue(arg) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if arg == "ps" {
			return i
		}
		return -1
	}
	return -1
}

func composeGlobalFlagTakesValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}

	switch arg {
	case "--ansi", "--env-file", "-f", "--file", "--parallel", "-p", "--project-name", "--profile", "--progress", "--project-directory":
		return true
	default:
		return false
	}
}

func psFormat(args []string, psIndex int) (string, bool) {
	for i := psIndex + 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--format" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		if strings.HasPrefix(arg, "--format=") {
			return strings.TrimPrefix(arg, "--format="), true
		}
	}
	return "", false
}

func runPS(ctx context.Context, stdout io.Writer, stderr io.Writer, targets []target, opts options, composeArgs []string, runner composeRunner) int {
	jsonResults := executeTargets(ctx, targets, psJSONArgs(composeArgs), opts.jobs, runner)
	rows, err := mergePSRows(jsonResults)
	if err == nil {
		if len(rows) == 0 {
			fmt.Fprintln(stdout, "No containers found.")
			return 0
		}
		fmt.Fprint(stdout, renderPSTable(rows))
		return 0
	}

	fallbackResults := executeTargets(ctx, targets, composeArgs, opts.jobs, runner)
	merged := mergePSText(fallbackResults)
	if merged != "" {
		fmt.Fprint(stdout, merged)
	}

	if failures := failureResults(fallbackResults); len(failures) > 0 {
		writeFailureSummary(stderr, failures)
		return failures[0].exitCode
	}

	return 0
}

func psJSONArgs(args []string) []string {
	psIndex := psCommandIndex(args)
	if psIndex < 0 {
		return append([]string(nil), args...)
	}

	if format, hasFormat := psFormat(args, psIndex); hasFormat && strings.EqualFold(format, "json") {
		return append([]string(nil), args...)
	}

	updated := make([]string, 0, len(args)+2)
	updated = append(updated, args[:psIndex+1]...)
	updated = append(updated, "--format", "json")
	updated = append(updated, args[psIndex+1:]...)
	return updated
}

func mergePSRows(results []commandResult) ([]psRow, error) {
	merged := make(map[string]psRow)

	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}

		entries, err := parsePSJSON(result.stdout)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			key := firstNonEmpty(entry, "ID", "Id", "Name")
			if key == "" {
				continue
			}

			merged[key] = psRow{
				key:     key,
				Name:    firstNonEmpty(entry, "Name"),
				Service: firstNonEmpty(entry, "Service"),
				Status:  statusText(entry),
				Ports:   portsText(entry),
			}
		}
	}

	rows := make([]psRow, 0, len(merged))
	for _, row := range merged {
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].key < rows[j].key
		}
		return rows[i].Name < rows[j].Name
	})

	return rows, nil
}

func parsePSJSON(raw string) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	var list []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
		return list, nil
	}

	var single map[string]any
	if err := json.Unmarshal([]byte(trimmed), &single); err == nil {
		return []map[string]any{single}, nil
	}

	lines := strings.Split(trimmed, "\n")
	list = make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("parse docker compose ps json: %w", err)
		}
		list = append(list, item)
	}

	return list, nil
}

func firstNonEmpty(entry map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := entry[key]
		if !ok {
			continue
		}

		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}

	return ""
}

func statusText(entry map[string]any) string {
	state := firstNonEmpty(entry, "State", "Status")
	health := firstNonEmpty(entry, "Health")
	exitCode := firstNonEmpty(entry, "ExitCode")

	if health != "" && !strings.EqualFold(health, "none") {
		if state == "" {
			return health
		}
		return fmt.Sprintf("%s (%s)", state, health)
	}
	if state != "" {
		return state
	}
	if exitCode != "" {
		return "exit " + exitCode
	}
	return "unknown"
}

func portsText(entry map[string]any) string {
	publishers, ok := entry["Publishers"]
	if ok {
		if text := formatPublishers(publishers); text != "" {
			return text
		}
	}

	return firstNonEmpty(entry, "Ports")
}

func formatPublishers(value any) string {
	publishers, ok := value.([]any)
	if !ok || len(publishers) == 0 {
		return ""
	}

	ports := make([]string, 0, len(publishers))
	for _, publisher := range publishers {
		entry, ok := publisher.(map[string]any)
		if !ok {
			continue
		}

		host := firstNonEmpty(entry, "URL", "IP")
		published := firstNonEmpty(entry, "PublishedPort")
		target := firstNonEmpty(entry, "TargetPort")
		protocol := firstNonEmpty(entry, "Protocol")

		switch {
		case host != "" && published != "" && target != "" && protocol != "":
			ports = append(ports, fmt.Sprintf("%s:%s->%s/%s", host, published, target, strings.ToLower(protocol)))
		case published != "" && target != "" && protocol != "":
			ports = append(ports, fmt.Sprintf("%s->%s/%s", published, target, strings.ToLower(protocol)))
		case published != "":
			ports = append(ports, published)
		}
	}

	return strings.Join(ports, ", ")
}

func renderPSTable(rows []psRow) string {
	headers := []string{"NAME", "SERVICE", "STATUS", "PORTS"}
	widths := []int{len(headers[0]), len(headers[1]), len(headers[2]), len(headers[3])}

	for _, row := range rows {
		widths[0] = max(widths[0], len(row.Name))
		widths[1] = max(widths[1], len(row.Service))
		widths[2] = max(widths[2], len(row.Status))
		widths[3] = max(widths[3], len(row.Ports))
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%-*s  %-*s  %-*s  %-*s\n", widths[0], headers[0], widths[1], headers[1], widths[2], headers[2], widths[3], headers[3])
	for _, row := range rows {
		fmt.Fprintf(&out, "%-*s  %-*s  %-*s  %-*s\n", widths[0], row.Name, widths[1], row.Service, widths[2], row.Status, widths[3], row.Ports)
	}

	return out.String()
}

func mergePSText(results []commandResult) string {
	var lines []string
	var header string

	for _, result := range results {
		trimmed := strings.TrimSpace(result.stdout)
		if trimmed == "" {
			continue
		}

		resultLines := strings.Split(trimmed, "\n")
		for i, line := range resultLines {
			if i == 0 {
				if header == "" {
					header = line
				} else if line == header {
					continue
				}
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return ""
	}

	return strings.Join(lines, "\n") + "\n"
}

func writeStandardOutput(stdout io.Writer, results []commandResult, quiet bool) {
	first := true
	for _, result := range results {
		body := targetOutput(result, quiet)
		if body == "" {
			continue
		}

		if !first {
			fmt.Fprintln(stdout)
		}
		first = false
		fmt.Fprint(stdout, body)
	}
}

func targetOutput(result commandResult, quiet bool) string {
	trimmedStdout := strings.TrimSpace(result.stdout)
	trimmedStderr := strings.TrimSpace(result.stderr)
	if trimmedStdout == "" && trimmedStderr == "" && result.exitCode == 0 {
		return ""
	}

	var out strings.Builder
	if !quiet {
		fmt.Fprintf(&out, "[%s]\n", result.target.Label)
	}

	if trimmedStdout != "" {
		out.WriteString(trimmedStdout)
		out.WriteByte('\n')
	}
	if trimmedStderr != "" {
		out.WriteString(trimmedStderr)
		out.WriteByte('\n')
	}
	if trimmedStdout == "" && trimmedStderr == "" && result.exitCode != 0 {
		fmt.Fprintf(&out, "command failed with exit code %d\n", result.exitCode)
	}

	return out.String()
}

func failureResults(results []commandResult) []commandResult {
	failures := make([]commandResult, 0)
	for _, result := range results {
		if result.exitCode != 0 {
			failures = append(failures, result)
		}
	}
	return failures
}

func writeFailureSummary(stderr io.Writer, failures []commandResult) {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		detail := firstLine(strings.TrimSpace(failure.stderr))
		if detail == "" {
			detail = firstLine(strings.TrimSpace(failure.stdout))
		}
		if detail == "" {
			detail = fmt.Sprintf("exit %d", failure.exitCode)
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", failure.target.Label, detail))
	}

	if len(parts) == 0 {
		return
	}

	fmt.Fprintf(stderr, "%d target(s) failed: %s\n", len(parts), strings.Join(parts, ", "))
}

func firstLine(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	return lines[0]
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

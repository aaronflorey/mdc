// Package discover finds the compose projects below a directory.
package discover

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var composeFilenames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// Target is one discovered compose project.
type Target struct {
	// Dir is the project directory.
	Dir string
	// File is the canonical compose file in Dir.
	File string
	// Label is Dir relative to the scan root ("." for the root itself).
	Label string
	// ProjectName is the compose project name used for the target.
	ProjectName string
}

// Result holds the discovered targets and the directories that were skipped.
type Result struct {
	Targets []Target
	// Warnings describes unreadable directories that were skipped.
	Warnings []string
}

// Targets scans root and its subdirectories down to maxDepth for compose files.
// Hidden directories are ignored and each directory contributes at most one
// compose file, chosen by filename precedence.
func Targets(root string, maxDepth int) (Result, error) {
	var result Result

	dirs, err := collectDirectories(root, maxDepth, &result.Warnings)
	if err != nil {
		return Result{}, err
	}

	projectNameCounts := make(map[string]int)
	for _, dir := range dirs {
		file, ok, err := canonicalComposeFile(dir, &result.Warnings)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			continue
		}

		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return Result{}, err
		}

		label := "."
		if rel != "." {
			label = filepath.ToSlash(rel)
		}

		projectName := defaultProjectName(dir)
		projectNameCounts[projectName]++

		result.Targets = append(result.Targets, Target{Dir: dir, File: file, Label: label, ProjectName: projectName})
	}

	for i := range result.Targets {
		if projectNameCounts[result.Targets[i].ProjectName] > 1 {
			result.Targets[i].ProjectName = uniqueProjectName(result.Targets[i].Dir)
		}
	}

	sort.Slice(result.Targets, func(i, j int) bool {
		return result.Targets[i].Label < result.Targets[j].Label
	})

	return result, nil
}

func collectDirectories(root string, maxDepth int, warnings *[]string) ([]string, error) {
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
			if current.path == root {
				return nil, fmt.Errorf("read directory %q: %w", current.path, err)
			}
			*warnings = append(*warnings, fmt.Sprintf("skipping unreadable directory %q: %v", current.path, err))
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
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

func canonicalComposeFile(dir string, warnings *[]string) (string, bool, error) {
	for _, name := range composeFilenames {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			if os.IsPermission(err) {
				*warnings = append(*warnings, fmt.Sprintf("skipping unreadable directory %q: %v", dir, err))
				return "", false, nil
			}
			return "", false, fmt.Errorf("stat compose file %q: %w", candidate, err)
		}
	}

	return "", false, nil
}

// defaultProjectName mirrors compose's default: the sanitized directory name.
func defaultProjectName(dir string) string {
	base := sanitizeProjectComponent(filepath.Base(filepath.Clean(dir)))
	if base == "" {
		return "root"
	}
	return base
}

// uniqueProjectName derives a stable name from the absolute directory, used
// when several targets would otherwise share the same default name.
func uniqueProjectName(dir string) string {
	cleanedDir := filepath.Clean(dir)
	if absoluteDir, err := filepath.Abs(cleanedDir); err == nil {
		cleanedDir = absoluteDir
	}

	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(filepath.ToSlash(cleanedDir)))

	return fmt.Sprintf("mdc-%s-%x", defaultProjectName(cleanedDir), hasher.Sum64())
}

func sanitizeProjectComponent(value string) string {
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

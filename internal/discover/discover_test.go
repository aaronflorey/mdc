package discover

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func labels(targets []Target) []string {
	out := make([]string, 0, len(targets))
	for _, tg := range targets {
		out = append(out, tg.Label)
	}
	return out
}

func TestTargetsFindsDepthOneComposeFiles(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "docker-compose.yml"))
	mustWriteFile(t, filepath.Join(root, "api", "nested", "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, ".git", "compose.yaml"))

	res, err := Targets(root, 1)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if got := strings.Join(labels(res.Targets), ","); got != ".,api" {
		t.Fatalf("unexpected labels: %s", got)
	}
	if res.Targets[0].ProjectName != defaultProjectName(root) || res.Targets[1].ProjectName != "api" {
		t.Fatalf("unexpected project names: %+v", res.Targets)
	}
}

func TestTargetsUsesCanonicalComposeFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yml"))
	mustWriteFile(t, filepath.Join(root, "docker-compose.yml"))

	res, err := Targets(root, 0)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(res.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(res.Targets))
	}
	if got := filepath.Base(res.Targets[0].File); got != "compose.yml" {
		t.Fatalf("expected compose.yml to win precedence, got %s", got)
	}
}

func TestTargetsRejectsNegativeDepth(t *testing.T) {
	if _, err := Targets(t.TempDir(), -1); err == nil {
		t.Fatal("expected error for negative depth")
	}
}

func TestUniqueProjectNameIsStableAndUnique(t *testing.T) {
	sep := string(os.PathSeparator)
	first := uniqueProjectName(filepath.Join(sep, "tmp", "services", "app"))
	second := uniqueProjectName(filepath.Join(sep, "srv", "examples", "app"))
	repeated := uniqueProjectName(filepath.Join(sep, "tmp", "services", "app"))

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

func TestDefaultProjectNameUsesDirectoryBasename(t *testing.T) {
	if got := defaultProjectName(filepath.Join(string(os.PathSeparator), "tmp", "Manual Demo")); got != "manual-demo" {
		t.Fatalf("expected manual-demo, got %q", got)
	}
}

func TestTargetsDisambiguatesDuplicateProjectNames(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "services", "app", "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "examples", "app", "compose.yaml"))

	res, err := Targets(root, 2)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(res.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(res.Targets))
	}
	a, b := res.Targets[0].ProjectName, res.Targets[1].ProjectName
	if a == b {
		t.Fatalf("expected distinct project names, got %q", a)
	}
	if !strings.HasPrefix(a, "mdc-app-") || !strings.HasPrefix(b, "mdc-app-") {
		t.Fatalf("expected hashed duplicate project names, got %q and %q", a, b)
	}
}

func TestTargetsSkipsUnreadableSubdirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "compose.yaml"))
	mustWriteFile(t, filepath.Join(root, "api", "compose.yaml"))
	locked := filepath.Join(root, "locked")
	mustWriteFile(t, filepath.Join(locked, "compose.yaml"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// Depth 1 hits the stat path, depth 2 the ReadDir path.
	for _, depth := range []int{1, 2} {
		res, err := Targets(root, depth)
		if err != nil {
			t.Fatalf("depth %d: unreadable subdir should be skipped: %v", depth, err)
		}
		got := labels(res.Targets)
		sort.Strings(got)
		if strings.Join(got, ",") != ".,api" {
			t.Fatalf("depth %d: expected [., api], got %v", depth, got)
		}
		if len(res.Warnings) == 0 {
			t.Fatalf("depth %d: expected a warning for the skipped directory", depth)
		}
	}
}

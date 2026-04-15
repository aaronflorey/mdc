//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCLIWithRealDockerCompose(t *testing.T) {
	requireDockerCompose(t)

	root := t.TempDir()
	rootCompose := filepath.Join(root, "compose.yaml")
	apiDir := filepath.Join(root, "api")
	apiCompose := filepath.Join(apiDir, "compose.yaml")

	mustWriteComposeFixture(t, rootCompose, "rootsvc")
	mustWriteComposeFixture(t, apiCompose, "apisvc")

	t.Cleanup(func() {
		dockerComposeDown(t, filepath.Dir(apiCompose), apiCompose)
		dockerComposeDown(t, filepath.Dir(rootCompose), rootCompose)
	})

	withWorkingDirectory(t, root, func() {
		var upStdout bytes.Buffer
		var upStderr bytes.Buffer
		if code := runCLI(context.Background(), &upStdout, &upStderr, []string{"up", "-d"}, execCompose); code != 0 {
			t.Fatalf("mdc up -d failed with code %d\nstdout:\n%s\nstderr:\n%s", code, upStdout.String(), upStderr.String())
		}

		var psStdout string
		deadline := time.Now().Add(20 * time.Second)
		for {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := runCLI(context.Background(), &stdout, &stderr, []string{"ps"}, execCompose)
			if code == 0 {
				psStdout = stdout.String()
				if countPSRows(psStdout) == 2 {
					break
				}
			}

			if time.Now().After(deadline) {
				t.Fatalf("mdc ps did not show both containers\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}

			time.Sleep(500 * time.Millisecond)
		}

		if strings.Count(psStdout, "NAME") != 1 {
			t.Fatalf("expected one ps header, got:\n%s", psStdout)
		}
		if !strings.Contains(psStdout, "rootsvc") || !strings.Contains(psStdout, "apisvc") {
			t.Fatalf("expected both services in merged ps output, got:\n%s", psStdout)
		}

		var downStdout bytes.Buffer
		var downStderr bytes.Buffer
		if code := runCLI(context.Background(), &downStdout, &downStderr, []string{"down", "--remove-orphans", "--volumes"}, execCompose); code != 0 {
			t.Fatalf("mdc down failed with code %d\nstdout:\n%s\nstderr:\n%s", code, downStdout.String(), downStderr.String())
		}
	})
}

func requireDockerCompose(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}

	cmd := exec.Command("docker", "compose", "version")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("docker compose is unavailable: %v\n%s", err, string(output))
	}
}

func mustWriteComposeFixture(t *testing.T, path string, serviceName string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}

	content := fmt.Sprintf(`services:
  %s:
    image: busybox:1.36.1
    command:
      - sh
      - -c
      - while true; do sleep 1; done
`, serviceName)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func dockerComposeDown(t *testing.T, dir string, composeFile string) {
	t.Helper()

	cmd := exec.Command("docker", "compose", "-f", composeFile, "--project-directory", dir, "down", "--remove-orphans", "--volumes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cleanup failed for %s: %v\n%s", dir, err, string(output))
	}
}

func withWorkingDirectory(t *testing.T, dir string, fn func()) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}

	fn()
}

func countPSRows(output string) int {
	count := 0
	for index, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if index == 0 {
			continue
		}
		count++
	}
	return count
}

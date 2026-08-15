//go:build smoke

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestTTYLiveStatusBoard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty not supported on windows")
	}

	root := t.TempDir()

	fakeDocker := filepath.Join(root, "docker")
	fakeDockerScript := `#!/bin/sh
printf '%s\r\n' '{"id":"Image nginx","status":"Working","text":"Pulling","percent":30}' >&2
sleep 0.05
printf '%s\r\n' '{"id":"Container fake-svc-1","status":"Working","text":"Creating"}' >&2
sleep 0.05
printf '%s\r\n' '{"id":"Container fake-svc-1","status":"Warning","text":"restarting"}' >&2
sleep 0.05
printf '%s\r\n' '{"id":"Container fake-svc-1","status":"Done","text":"Started"}' >&2
`
	if err := os.WriteFile(fakeDocker, []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	svc1 := filepath.Join(root, "svc1")
	svc2 := filepath.Join(root, "svc2")
	for _, d := range []string{svc1, svc2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(svc1, "compose.yaml"), []byte("services:\n  app:\n    image: busybox\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	if err := os.WriteFile(filepath.Join(svc2, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}

	t.Setenv("PATH", root+string(filepath.ListSeparator)+os.Getenv("PATH"))

	ptyMaster, ptySlave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptySlave.Close()

	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [4096]byte
		for {
			n, readErr := ptyMaster.Read(buf[:])
			if n > 0 {
				output.Write(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	var stderr bytes.Buffer
	code := runCLI(context.Background(), ptySlave, &stderr, []string{"up", "-d"}, execCompose)

	ptySlave.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		ptyMaster.Close()
		<-done
	}
	ptyMaster.Close()

	out := output.String()

	if code != 0 {
		t.Errorf("exit code: %d, stderr: %s", code, stderr.String())
	}

	if !strings.Contains(out, "Running 2 compose projects") {
		t.Errorf("missing board start message:\n%s", out)
	}

	if !strings.Contains(out, "\x1b[") {
		t.Errorf("missing ANSI escape sequences (live board not rendered):\n%s", out)
	}

	if !strings.Contains(out, "Creating") {
		t.Errorf("missing JSON progress create status:\n%s", out)
	}

	if !strings.Contains(out, "Pulling (30%)") {
		t.Errorf("missing percent-bearing pull status:\n%s", out)
	}

	if !strings.Contains(out, "Warning: restarting (Container fake-svc-1)") {
		t.Errorf("missing JSON progress warning status:\n%s", out)
	}

	if !strings.Contains(out, "Started") {
		t.Errorf("missing JSON progress done status:\n%s", out)
	}

	if !strings.Contains(out, "[svc1] up complete") {
		t.Errorf("missing svc1 up complete:\n%s", out)
	}
	if !strings.Contains(out, "[svc2] up complete") {
		t.Errorf("missing svc2 up complete:\n%s", out)
	}

}

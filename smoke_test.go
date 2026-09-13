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
	const terminalColumns = 64
	const humanizedImage = "ghcr.io/acme/long-compose-image-name:latest"
	const warningMessage = `Warning: The "version" field is obsolete and will be ignored. It's recommended to remove it to avoid potential confusion`

	fakeDocker := filepath.Join(root, "docker")
	fakeDockerScript := `#!/bin/sh
printf '%s\r\n' '{"id":"Image ghcr.io/acme/long-compose-image-name:latest@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","status":"Working","text":"Pulling","percent":30}' >&2
sleep 0.05
printf '%s\r\n' '{"level":"warning","msg":"The \"version\" field is obsolete and will be ignored. It'"'"'s recommended to remove it to avoid potential confusion","time":"2026-08-15T10:00:00Z"}' >&2
printf '%s\r\n' '{"level":"warning","msg":"The \"version\" field is obsolete and will be ignored. It'"'"'s recommended to remove it to avoid potential confusion","time":"2026-08-15T10:00:01Z"}' >&2
sleep 0.05
printf '%s\r\n' '{"id":"Container fake-svc-1","status":"Working","text":"Creating"}' >&2
sleep 0.05
printf '%s\r\n' '{"id":"Container fake-svc-1","status":"Warning","text":"restarting"}' >&2
sleep 0.05
printf '%s\r\n' '{"id":"Image registry.example.com/acme/another-long-compose-image:stable@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","status":"Working","text":"Pulled"}' >&2
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
	if err := pty.Setsize(ptySlave, &pty.Winsize{Rows: 24, Cols: terminalColumns}); err != nil {
		t.Fatalf("pty.Setsize: %v", err)
	}

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

	if !strings.Contains(out, humanizedImage+" Pulling (30%)") {
		t.Errorf("missing humanized percent-bearing pull status for %q:\n%s", humanizedImage, out)
	}

	if !strings.Contains(out, "Warning: restarting (Container fake-svc-1)") {
		t.Errorf("missing JSON progress warning status:\n%s", out)
	}

	if !strings.Contains(out, "Started") {
		t.Errorf("missing JSON progress done status:\n%s", out)
	}

	if !strings.Contains(out, "2 targets succeeded, 0 targets failed") {
		t.Errorf("missing aggregate completion summary:\n%s", out)
	}

	frames, complete := liveBoardFrames(out)
	if !complete {
		t.Fatalf("could not parse a complete live board frame:\n%s", out)
	}
	if len(frames) == 0 {
		t.Fatalf("missing live board frames:\n%s", out)
	}
	expectedLabels := map[string]bool{"[svc1]": true, "[svc2]": true}
	for frameIndex, rows := range frames {
		if len(rows) == 0 {
			continue
		}
		allRowsBlank := true
		for _, row := range rows {
			if row != "" {
				allRowsBlank = false
				break
			}
		}
		if allRowsBlank {
			continue
		}
		if len(rows) != len(expectedLabels) {
			t.Errorf("frame %d has %d rows, want one per target: %q", frameIndex, len(rows), rows)
			continue
		}

		seenLabels := make(map[string]bool, len(rows))
		for _, row := range rows {
			if displayWidth(row) > terminalColumns {
				t.Errorf("frame %d row exceeds terminal width %d: %q", frameIndex, terminalColumns, row)
			}
			labelEnd := strings.IndexByte(row, ']')
			if !strings.HasPrefix(row, "[") || labelEnd < 0 {
				t.Errorf("frame %d row has no target label: %q", frameIndex, row)
				continue
			}
			label := row[:labelEnd+1]
			if !expectedLabels[label] {
				t.Errorf("frame %d has unexpected target row %q", frameIndex, row)
			}
			if seenLabels[label] {
				t.Errorf("frame %d repeats target row %q", frameIndex, row)
			}
			seenLabels[label] = true
		}
		for label := range expectedLabels {
			if !seenLabels[label] {
				t.Errorf("frame %d is missing target row %s", frameIndex, label)
			}
		}
	}

	if strings.Contains(out, "sha256:") {
		t.Errorf("image digest leaked into live display:\n%s", out)
	}

	wantWarnings := "Warnings:\n[svc1]\n" + warningMessage + "\n[svc2]\n" + warningMessage + "\n"
	if got := stderr.String(); got != wantWarnings {
		t.Errorf("expected grouped, deduplicated warning report %q, got %q", wantWarnings, got)
	}
}

func liveBoardFrames(output string) ([][]string, bool) {
	var frames [][]string
	complete := true
	for _, encodedFrame := range strings.Split(output, "\x1b[2A") {
		rows, frameComplete := liveBoardFrameRows(encodedFrame)
		if !frameComplete {
			complete = false
			continue
		}
		if len(rows) == 0 {
			continue
		}
		frames = append(frames, rows)
	}
	return frames, complete
}

func liveBoardFrameRows(encodedFrame string) ([]string, bool) {
	const rowPrefix = "\r\x1b[2K"
	var rows []string
	for {
		rowStart := strings.Index(encodedFrame, rowPrefix)
		if rowStart < 0 {
			return rows, true
		}
		encodedFrame = encodedFrame[rowStart+len(rowPrefix):]
		lineEnd := strings.IndexByte(encodedFrame, '\n')
		if lineEnd < 0 {
			return nil, false
		}
		rows = append(rows, strings.TrimSuffix(encodedFrame[:lineEnd], "\r"))
		encodedFrame = encodedFrame[lineEnd+1:]
	}
}

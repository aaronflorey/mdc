package ui

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestLogStreamAlignsPrefixesAcrossProjects(t *testing.T) {
	var out bytes.Buffer
	s := NewLogStream(LogStreamOptions{Out: &out, Err: &out, Prefix: true})
	short := s.Consumer("a", map[string]int{"web": 1})
	long := s.Consumer("longer", map[string]int{"worker": 2})

	short.Log("web-1", "hello")
	long.Log("worker-2", "world")

	want := "a/web           │ hello\nlonger/worker-2 │ world\n"
	if out.String() != want {
		t.Fatalf("got\n%q\nwant\n%q", out.String(), want)
	}
}

func TestLogStreamPrefixOptions(t *testing.T) {
	var out bytes.Buffer
	s := NewLogStream(LogStreamOptions{Out: &out, Err: &out})
	s.Consumer("p", map[string]int{"web": 1}).Log("web-1", "one\ntwo")
	if out.String() != "one\ntwo\n" {
		t.Fatalf("got %q", out.String())
	}

	out.Reset()
	s = NewLogStream(LogStreamOptions{Out: &out, Err: &out, Prefix: true, Timestamps: true})
	s.Consumer("p", map[string]int{"web": 1}).Log("web-1", "line")
	if got := out.String(); !strings.HasPrefix(got, "p/web │ 20") || !strings.HasSuffix(got, " line\n") {
		t.Fatalf("got %q", got)
	}
}

func TestLogStreamWritesWholeLinesConcurrently(t *testing.T) {
	var out bytes.Buffer
	s := NewLogStream(LogStreamOptions{Out: &out, Err: &out, Prefix: true})
	var wg sync.WaitGroup
	for i := range 8 {
		c := s.Consumer(fmt.Sprintf("p%d", i), map[string]int{"svc": 1})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				c.Log("svc-1", strings.Repeat("x", 50))
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 8*200 {
		t.Fatalf("got %d lines", len(lines))
	}
	for _, l := range lines {
		if !strings.HasSuffix(l, " │ "+strings.Repeat("x", 50)) {
			t.Fatalf("torn line %q", l)
		}
	}
}

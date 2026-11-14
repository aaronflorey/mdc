package ui

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/docker/compose/v5/pkg/api"
)

// timestampLayout matches the fixed-width timestamps compose puts before lines.
const timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// LogStreamOptions choose how a LogStream decorates the lines it writes.
type LogStreamOptions struct {
	// Out receives container output and status lines; Err receives container
	// stderr. Both are written to one whole line at a time.
	Out, Err io.Writer
	// Color colours the prefix per project and service.
	Color bool
	// Prefix puts "project/service │" before each line.
	Prefix bool
	// Timestamps puts the time a line was written before its text.
	Timestamps bool
}

// LogStream interleaves the output of containers from many projects into one
// prefixed stream. It is safe for concurrent use and writes whole lines only.
type LogStream struct {
	opts LogStreamOptions

	mu    sync.Mutex
	width int
}

// NewLogStream creates a stream writing to opts.Out and opts.Err.
func NewLogStream(opts LogStreamOptions) *LogStream {
	return &LogStream{opts: opts}
}

// Terminal returns a writer for f that downgrades colours to what the
// terminal supports, and drops them under NO_COLOR.
func Terminal(f *os.File) io.Writer { return terminalWriter{f} }

type terminalWriter struct{ f *os.File }

func (w terminalWriter) Write(p []byte) (int, error) {
	if _, err := lipgloss.Fprint(w.f, string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Consumer returns the log consumer of one project. replicas maps each of the
// project's services to how many containers it runs; the container index is
// shown in prefixes only for services with more than one. Its names are
// reserved up front so prefixes line up from the first line.
func (s *LogStream) Consumer(project string, replicas map[string]int) api.LogConsumer {
	c := &projectLogs{stream: s, project: project, replicas: replicas}
	s.mu.Lock()
	defer s.mu.Unlock()
	for service, n := range replicas {
		if n <= 1 {
			s.reserve(c.label(service))
			continue
		}
		for i := 1; i <= n; i++ {
			s.reserve(c.label(service + "-" + strconv.Itoa(i)))
		}
	}
	return c
}

// Raw writes text to Out unmodified, as one write.
func (s *LogStream) Raw(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.opts.Out, text)
}

func (s *LogStream) reserve(label string) {
	s.width = max(s.width, ansi.StringWidth(label))
}

type projectLogs struct {
	stream   *LogStream
	project  string
	replicas map[string]int
}

// label is the text of a prefix: the project and the container's display name.
func (c *projectLogs) label(name string) string {
	return c.project + "/" + name
}

// display is the container name as shown: the index is dropped for services
// that run a single container.
func (c *projectLogs) display(container string) string {
	if container == api.WatchLogger {
		return "watch"
	}
	service, index, ok := splitIndex(container)
	if ok && index == "1" && c.replicas[service] <= 1 {
		return service
	}
	return container
}

// splitIndex splits "web-2" into "web" and "2".
func splitIndex(container string) (service, index string, ok bool) {
	i := strings.LastIndexByte(container, '-')
	if i <= 0 {
		return container, "", false
	}
	if _, err := strconv.Atoi(container[i+1:]); err != nil {
		return container, "", false
	}
	return container[:i], container[i+1:], true
}

func (c *projectLogs) Log(container, message string) {
	c.write(c.stream.opts.Out, container, message, false)
}
func (c *projectLogs) Err(container, message string) {
	c.write(c.stream.opts.Err, container, message, false)
}
func (c *projectLogs) Status(container, message string) {
	c.write(c.stream.opts.Out, container, message, true)
}

func (c *projectLogs) write(w io.Writer, container, message string, status bool) {
	s := c.stream
	label := c.label(c.display(container))
	service, _, _ := splitIndex(container)
	paint := func(style Style, text string) string {
		if !s.opts.Color {
			return text
		}
		return style.Render(text)
	}
	now := time.Now().Format(timestampLayout)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserve(label)

	var prefix string
	switch {
	case s.opts.Prefix:
		pad := strings.Repeat(" ", s.width-ansi.StringWidth(label))
		prefix = paint(NameStyle(c.project+"/"+service), label+pad+" │") + " "
	case status:
		prefix = label + " "
	}
	if s.opts.Timestamps {
		prefix += now + " "
	}

	var b strings.Builder
	for line := range strings.SplitSeq(message, "\n") {
		if status {
			line = paint(Dim, line)
		}
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	_, _ = io.WriteString(w, b.String())
}

// Notice writes a dim line about a project as a whole.
func (s *LogStream) Notice(project, text string) {
	line := project + " " + text
	if s.opts.Color {
		line = Dim.Render(line)
	}
	s.Raw(line + "\n")
}

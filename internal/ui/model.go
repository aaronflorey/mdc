package ui

import (
	"context"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/docker/compose/v5/pkg/api"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
)

type (
	eventsMsg struct {
		idx    int
		events []api.Resource
	}
	finishMsg struct {
		idx     int
		outcome runner.Outcome
	}
	promptMsg struct {
		idx     int
		message string
		def     bool
		reply   chan<- bool
	}
	// cancelMsg requests cancellation, like pressing ctrl+c.
	cancelMsg struct{}
	// doneMsg ends the program once all work has returned.
	doneMsg struct{}
)

type promptState struct {
	idx     int
	message string
	def     bool
	reply   chan<- bool
}

type model struct {
	projects   []*projectState
	spinner    spinner.Model
	bar        progress.Model
	width      int
	height     int
	prompt     *promptState
	cancel     context.CancelFunc
	cancelling bool
	done       bool
}

func newModel(targets []discover.Target, cancel context.CancelFunc) *model {
	projects := make([]*projectState, len(targets))
	for i, t := range targets {
		projects[i] = newProjectState(t)
	}
	return &model{
		projects: projects,
		spinner:  spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(Header)),
		bar:      progress.New(),
		width:    80,
		height:   24,
		cancel:   cancel,
	}
}

func (m *model) Init() tea.Cmd { return m.spinner.Tick }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case eventsMsg:
		m.projects[msg.idx].apply(msg.events...)
	case finishMsg:
		if p := m.projects[msg.idx]; p.running() {
			o := msg.outcome
			p.outcome = &o
		}
	case promptMsg:
		m.prompt = &promptState{idx: msg.idx, message: msg.message, def: msg.def, reply: msg.reply}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case cancelMsg:
		m.interrupt()
	case doneMsg:
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		m.interrupt()
		return nil
	}
	if m.prompt == nil {
		return nil
	}
	switch msg.String() {
	case "y", "Y":
		m.answer(true)
	case "n", "N":
		m.answer(false)
	case "enter":
		m.answer(m.prompt.def)
	}
	return nil
}

func (m *model) answer(yes bool) {
	m.prompt.reply <- yes
	m.prompt = nil
}

// interrupt cancels the context and declines any open question.
func (m *model) interrupt() {
	m.cancelling = true
	m.cancel()
	if m.prompt != nil {
		m.answer(false)
	}
}

func (m *model) View() tea.View {
	if m.done {
		// The summary is printed after the program exits so it is not
		// clipped to the screen height and stays in scrollback.
		return tea.NewView("")
	}
	return tea.NewView(m.render())
}

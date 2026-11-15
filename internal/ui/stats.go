package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/docker/go-units"
)

// Usage is the resource consumption of one or several containers.
type Usage struct {
	CPU        float64
	Memory     float64
	NetRx      float64
	NetTx      float64
	BlockRead  float64
	BlockWrite float64
	PIDs       uint64
}

func (u *Usage) add(o Usage) {
	u.CPU += o.CPU
	u.Memory += o.Memory
	u.NetRx += o.NetRx
	u.NetTx += o.NetTx
	u.BlockRead += o.BlockRead
	u.BlockWrite += o.BlockWrite
	u.PIDs += o.PIDs
}

// ContainerStats is one container's row. Usage is meaningful once Sampled.
type ContainerStats struct {
	ID       string
	Project  string
	Name     string
	MemLimit float64
	Sampled  bool
	Usage
}

// ContainerStatsView is a container row formatted for display and JSON output.
type ContainerStatsView struct {
	Project  string
	Name     string
	CPUPerc  string
	MemUsage string
	MemPerc  string
	NetIO    string
	BlockIO  string
	PIDs     uint64
}

// ProjectStatsView is a project row formatted for display and JSON output.
type ProjectStatsView struct {
	Project    string
	Containers int
	CPUPerc    string
	MemUsage   string
	NetIO      string
	BlockIO    string
	PIDs       uint64
}

// statsTable collects the rows of containers as listings and samples arrive.
type statsTable struct {
	rows map[string]ContainerStats
}

// list replaces the set of containers. Containers already known keep their
// last sample until a new one arrives.
func (t *statsTable) list(items []ContainerStats) {
	next := make(map[string]ContainerStats, len(items))
	for _, item := range items {
		if old, ok := t.rows[item.ID]; ok && !item.Sampled {
			item.Sampled, item.MemLimit, item.Usage = old.Sampled, old.MemLimit, old.Usage
		}
		next[item.ID] = item
	}
	t.rows = next
}

// sample records a container's usage; samples of unlisted containers are dropped.
func (t *statsTable) sample(id string, memLimit float64, u Usage) {
	row, ok := t.rows[id]
	if !ok {
		return
	}
	row.Sampled, row.MemLimit, row.Usage = true, memLimit, u
	t.rows[id] = row
}

// sorted returns the rows ordered by project and then name.
func (t *statsTable) sorted() []ContainerStats {
	rows := make([]ContainerStats, 0, len(t.rows))
	for _, r := range t.rows {
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(a, b ContainerStats) int {
		return cmp.Or(strings.Compare(a.Project, b.Project), strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	return rows
}

// ContainerViews formats rows; containers not sampled yet show no figures.
func ContainerViews(rows []ContainerStats) []ContainerStatsView {
	views := make([]ContainerStatsView, len(rows))
	for i, r := range rows {
		views[i] = ContainerStatsView{Project: r.Project, Name: r.Name}
		if !r.Sampled {
			views[i].CPUPerc, views[i].MemUsage, views[i].MemPerc, views[i].NetIO, views[i].BlockIO = "--", "--", "--", "--", "--"
			continue
		}
		memPerc := 0.0
		if r.MemLimit > 0 {
			memPerc = r.Memory / r.MemLimit * 100
		}
		views[i].CPUPerc = fmt.Sprintf("%.2f%%", r.CPU)
		views[i].MemUsage = units.BytesSize(r.Memory) + " / " + units.BytesSize(r.MemLimit)
		views[i].MemPerc = fmt.Sprintf("%.2f%%", memPerc)
		views[i].NetIO = ioString(r.NetRx, r.NetTx)
		views[i].BlockIO = ioString(r.BlockRead, r.BlockWrite)
		views[i].PIDs = r.PIDs
	}
	return views
}

// ProjectViews sums the rows of each project. rows must be sorted by project.
func ProjectViews(rows []ContainerStats) []ProjectStatsView {
	var names []string
	counts := map[string]int{}
	totals := map[string]*Usage{}
	for _, r := range rows {
		if _, ok := totals[r.Project]; !ok {
			names = append(names, r.Project)
			totals[r.Project] = &Usage{}
		}
		counts[r.Project]++
		totals[r.Project].add(r.Usage)
	}
	views := make([]ProjectStatsView, len(names))
	for i, name := range names {
		u := totals[name]
		views[i] = ProjectStatsView{
			Project:    name,
			Containers: counts[name],
			CPUPerc:    fmt.Sprintf("%.2f%%", u.CPU),
			MemUsage:   units.BytesSize(u.Memory),
			NetIO:      ioString(u.NetRx, u.NetTx),
			BlockIO:    ioString(u.BlockRead, u.BlockWrite),
			PIDs:       u.PIDs,
		}
	}
	return views
}

func ioString(in, out float64) string {
	return units.HumanSizeWithPrecision(in, 3) + " / " + units.HumanSizeWithPrecision(out, 3)
}

// RenderStats lays out the rows as a table, per project when aggregate is set.
func RenderStats(rows []ContainerStats, aggregate bool, width int) string {
	if aggregate {
		views := ProjectViews(rows)
		cols := []Column{{Title: "PROJECT"}, {Title: "CONTAINERS"}, {Title: "CPU %"}, {Title: "MEM USAGE"}, {Title: "NET I/O", Drop: 2}, {Title: "BLOCK I/O", Drop: 1}, {Title: "PIDS", Drop: 3}}
		cells := make([][]string, len(views))
		for i, v := range views {
			cells[i] = []string{projectCell(v.Project), fmt.Sprint(v.Containers), v.CPUPerc, v.MemUsage, v.NetIO, v.BlockIO, fmt.Sprint(v.PIDs)}
		}
		return RenderTable(cols, cells, width)
	}
	views := ContainerViews(rows)
	cols := []Column{{Title: "PROJECT"}, {Title: "NAME"}, {Title: "CPU %"}, {Title: "MEM USAGE / LIMIT"}, {Title: "MEM %", Drop: 4}, {Title: "NET I/O", Drop: 2}, {Title: "BLOCK I/O", Drop: 1}, {Title: "PIDS", Drop: 3}}
	cells := make([][]string, len(views))
	for i, v := range views {
		cells[i] = []string{projectCell(v.Project), v.Name, v.CPUPerc, v.MemUsage, v.MemPerc, v.NetIO, v.BlockIO, fmt.Sprint(v.PIDs)}
	}
	return RenderTable(cols, cells, width)
}

func projectCell(project string) string { return NameStyle(project).Render(project) }

// StatsSink receives the containers and samples a collector finds.
type StatsSink struct{ program *tea.Program }

type (
	statsListMsg   []ContainerStats
	statsSampleMsg struct {
		id       string
		memLimit float64
		usage    Usage
	}
)

// List replaces the containers shown.
func (s StatsSink) List(items []ContainerStats) { s.program.Send(statsListMsg(items)) }

// Sample records one container's usage.
func (s StatsSink) Sample(id string, memLimit float64, u Usage) {
	s.program.Send(statsSampleMsg{id: id, memLimit: memLimit, usage: u})
}

type statsModel struct {
	table     statsTable
	aggregate bool
	width     int
	height    int
	cancel    context.CancelFunc
}

func (m *statsModel) Init() tea.Cmd { return nil }

func (m *statsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case statsListMsg:
		m.table.list(msg)
	case statsSampleMsg:
		m.table.sample(msg.id, msg.memLimit, msg.usage)
	case tea.KeyPressMsg:
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			m.cancel()
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *statsModel) View() tea.View {
	lines := strings.Split(RenderStats(m.table.sorted(), m.aggregate, m.width), "\n")
	if len(lines) > m.height-1 {
		lines = lines[:max(m.height-1, 1)]
	}
	return tea.NewView(strings.Join(lines, "\n"))
}

// RunStats shows a table that collect keeps up to date through the sink, until
// collect returns, the user quits (q or ctrl+c) or ctx is cancelled.
func RunStats(ctx context.Context, aggregate bool, collect func(ctx context.Context, sink StatsSink) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := &statsModel{aggregate: aggregate, width: TerminalWidth(), height: 24, cancel: cancel}
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.InterruptMsg); ok {
			cancel()
			return tea.Quit()
		}
		return msg
	}))

	collected := make(chan error, 1)
	go func() {
		err := collect(ctx, StatsSink{program})
		program.Quit()
		collected <- err
	}()
	_, runErr := program.Run()
	userQuit := ctx.Err() != nil
	cancel()
	collectErr := <-collected
	if errors.Is(collectErr, context.Canceled) {
		collectErr = nil
	}
	if runErr != nil && !userQuit {
		return runErr
	}
	return collectErr
}

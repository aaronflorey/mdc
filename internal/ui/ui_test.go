package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/docker/compose/v5/pkg/api"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
)

func target(name string) discover.Target {
	return discover.Target{Label: name, ProjectName: name, Dir: "/x/" + name}
}

func TestPullLayersAggregateIntoOneBar(t *testing.T) {
	p := newProjectState(target("a"))
	p.apply(
		api.Resource{ID: "Image web", Status: api.Working, Text: "Pulling"},
		api.Resource{ID: "l1", ParentID: "Image web", Status: api.Working, Current: 50, Total: 100},
		api.Resource{ID: "l2", ParentID: "Image web", Status: api.Working, Current: 0, Total: 300},
	)
	rows := p.rows()
	if len(rows) != 1 || !rows[0].Bar || rows[0].Percent != 12.5 {
		t.Fatalf("rows = %+v", rows)
	}

	// A finished layer usually reports no totals; it still counts as complete.
	p.apply(api.Resource{ID: "l1", ParentID: "Image web", Status: api.Done})
	if got := p.rows()[0].Percent; got != 25 {
		t.Fatalf("percent after l1 done = %v, want 25", got)
	}

	p.apply(api.Resource{ID: "Image web", Status: api.Done, Text: "Pulled"})
	if got := p.rows()[0].Percent; got != 100 {
		t.Fatalf("percent when parent done = %v", got)
	}
}

func TestAggregateFallsBackToPercent(t *testing.T) {
	p := newProjectState(target("a"))
	p.apply(
		api.Resource{ID: "l1", ParentID: "Image db", Percent: 20},
		api.Resource{ID: "l2", ParentID: "Image db", Status: api.Done},
	)
	rows := p.rows()
	if len(rows) != 1 || rows[0].ID != "Image db" || rows[0].Percent != 60 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestFailedLayerListedUnderImage(t *testing.T) {
	p := newProjectState(target("a"))
	p.apply(
		api.Resource{ID: "Image web"},
		api.Resource{ID: "l1", ParentID: "Image web", Status: api.Error, Details: "denied"},
		api.Resource{ID: "l2", ParentID: "Image web", Status: api.Done},
	)
	rows := p.rows()
	if len(rows) != 2 || rows[1].ID != "l1" || rows[1].Indent != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestEventsUpdateResourceInPlace(t *testing.T) {
	p := newProjectState(target("a"))
	p.apply(api.Resource{ID: "Container a-1", Status: api.Working, Text: "Creating"})
	p.apply(api.Resource{ID: "Network n", Status: api.Done})
	p.apply(api.Resource{ID: "Container a-1", Status: api.Done, Text: "Created"})
	rows := p.rows()
	if len(rows) != 2 || rows[0].ID != "Container a-1" || rows[0].Status != api.Done || rows[0].Text != "Created" {
		t.Fatalf("rows = %+v", rows)
	}
}

func update(m *model, msg tea.Msg) {
	_, _ = m.Update(msg)
}

func TestFinishKeepsFirstOutcomeAndTallies(t *testing.T) {
	m := newModel([]discover.Target{target("a"), target("b"), target("c"), target("d")}, func() {})
	update(m, finishMsg{0, runner.Outcome{Target: m.projects[0].target}})
	update(m, finishMsg{1, runner.Outcome{Status: runner.Skipped, Reason: "none"}})
	update(m, finishMsg{2, runner.Outcome{Status: runner.Failed, Err: errors.New("boom")}})
	update(m, finishMsg{2, runner.Outcome{}})

	if got := tally(m.projects); got != (counts{OK: 1, Skipped: 1, Failed: 1, Running: 1}) {
		t.Fatalf("counts = %+v", got)
	}
	if !strings.Contains(m.summary(), "boom") {
		t.Fatalf("summary lacks failure text:\n%s", m.summary())
	}
}

func TestPromptAnsweredByKey(t *testing.T) {
	m := newModel([]discover.Target{target("a")}, func() {})
	reply := make(chan bool, 1)
	update(m, promptMsg{idx: 0, message: "Remove?", def: true, reply: reply})
	update(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.prompt == nil {
		t.Fatal("unrelated key answered the prompt")
	}
	update(m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.prompt != nil || <-reply {
		t.Fatal("n should answer false and close the prompt")
	}

	update(m, promptMsg{idx: 0, def: true, reply: reply})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !<-reply {
		t.Fatal("enter should answer the default")
	}
}

func TestInterruptCancelsAndDeclinesPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := newModel([]discover.Target{target("a")}, cancel)
	reply := make(chan bool, 1)
	update(m, promptMsg{idx: 0, def: true, reply: reply})
	update(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	if ctx.Err() == nil || <-reply {
		t.Fatal("ctrl+c should cancel the context and decline the prompt")
	}
}

func TestViewFitsTerminal(t *testing.T) {
	var targets []discover.Target
	for _, n := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		targets = append(targets, target(n))
	}
	m := newModel(targets, func() {})
	for i := range targets {
		for j := 0; j < 8; j++ {
			update(m, eventsMsg{i, []api.Resource{{ID: strings.Repeat("Container-", 6) + string(rune('a'+j)), Status: api.Working}}})
		}
	}
	update(m, finishMsg{0, runner.Outcome{}})

	for _, size := range [][2]int{{40, 10}, {120, 30}, {20, 3}} {
		update(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		out := m.render()
		lines := strings.Split(out, "\n")
		if len(lines) > size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if w := lipgloss.Width(l); w > size[0] {
				t.Fatalf("%v: line width %d: %q", size, w, l)
			}
		}
	}
}

func TestImageBarNeverRunsBackwards(t *testing.T) {
	p := newProjectState(target("a"))
	p.apply(
		api.Resource{ID: "Image web", Status: api.Working},
		api.Resource{ID: "l1", ParentID: "Image web", Status: api.Working, Current: 100, Total: 100},
	)
	// Extraction restarts the layer's progress.
	p.apply(api.Resource{ID: "l1", ParentID: "Image web", Status: api.Working, Current: 10, Total: 100})
	if got := p.rows()[0].Percent; got != 100 {
		t.Fatalf("percent = %v, want 100", got)
	}
}

func TestSharedLayersBelongToEachImage(t *testing.T) {
	p := newProjectState(target("a"))
	p.apply(
		api.Resource{ID: "l1", ParentID: "Image a", Status: api.Working, Current: 10, Total: 100},
		api.Resource{ID: "l1", ParentID: "Image b", Status: api.Working, Current: 90, Total: 100},
	)
	rows := p.rows()
	if len(rows) != 2 || rows[0].Percent != 10 || rows[1].Percent != 90 {
		t.Fatalf("rows = %+v", rows)
	}
}

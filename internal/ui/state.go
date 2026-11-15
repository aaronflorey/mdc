package ui

import (
	"github.com/docker/compose/v5/pkg/api"

	"github.com/aaronflorey/mdc/internal/discover"
	"github.com/aaronflorey/mdc/internal/runner"
)

// projectState accumulates the resources reported for one target.
type projectState struct {
	target  discover.Target
	order   []string
	res     map[string]api.Resource
	outcome *runner.Outcome
	// peak is the highest aggregate progress seen per parent. Layers restart
	// from zero when they move from downloading to extracting, which would
	// otherwise make the image's bar run backwards.
	peak map[string]float64
}

func newProjectState(t discover.Target) *projectState {
	return &projectState{target: t, res: map[string]api.Resource{}, peak: map[string]float64{}}
}

func (p *projectState) running() bool { return p.outcome == nil }

// apply records events, keyed by resource ID. Layer events often omit totals
// once done, so previously seen totals are kept and done resources are
// treated as complete.
func (p *projectState) apply(events ...api.Resource) {
	touched := map[string]bool{}
	defer func() {
		for parent := range touched {
			p.trackPeak(parent)
		}
	}()
	for _, e := range events {
		key := e.ID
		if e.ParentID != "" {
			if _, ok := p.res[e.ParentID]; !ok {
				p.add(e.ParentID, api.Resource{ID: e.ParentID, Text: e.ParentID, Status: api.Working})
			}
			// Images in one project can share layers, so a layer is only
			// unique within its parent.
			key = e.ParentID + "\x00" + e.ID
		}
		old, seen := p.res[key]
		if seen {
			if e.Total == 0 {
				e.Total = old.Total
			}
			if e.Current == 0 && e.Status == api.Working {
				e.Current = old.Current
			}
		}
		if e.Status == api.Done && e.Total > 0 {
			e.Current = e.Total
		}
		if seen {
			p.res[key] = e
		} else {
			p.add(key, e)
		}
		if e.ParentID != "" {
			touched[e.ParentID] = true
		}
	}
}

// trackPeak records the aggregate progress of parent from its children.
func (p *projectState) trackPeak(parent string) {
	var kids []api.Resource
	for _, key := range p.order {
		if r := p.res[key]; r.ParentID == parent {
			kids = append(kids, r)
		}
	}
	p.peak[parent] = max(p.peak[parent], aggregatePercent(p.res[parent], kids))
}

func (p *projectState) add(key string, r api.Resource) {
	p.order = append(p.order, key)
	p.res[key] = r
}

// row is one displayable line of a running project.
type row struct {
	api.Resource
	Indent int
	// Bar is set for parents aggregating child progress; Percent is 0-100.
	Bar     bool
	Percent float64
}

// active reports whether the row still needs attention.
func (r row) active() bool { return r.Status != api.Done }

// rows builds the display rows: one per top-level resource, with a single
// aggregated bar for parents that have children, and only failed children
// listed beneath them.
func (p *projectState) rows() []row {
	children := map[string][]api.Resource{}
	for _, id := range p.order {
		if r := p.res[id]; r.ParentID != "" {
			children[r.ParentID] = append(children[r.ParentID], r)
		}
	}

	var rows []row
	for _, id := range p.order {
		r := p.res[id]
		if r.ParentID != "" {
			continue
		}
		kids := children[id]
		if len(kids) == 0 {
			rows = append(rows, row{Resource: r})
			continue
		}
		rows = append(rows, row{Resource: r, Bar: true, Percent: max(p.peak[id], aggregatePercent(r, kids))})
		for _, k := range kids {
			if k.Status == api.Error {
				rows = append(rows, row{Resource: k, Indent: 1})
			}
		}
	}
	return rows
}

// aggregatePercent combines child progress: summed bytes when children report
// totals, otherwise the mean of their percentages.
func aggregatePercent(parent api.Resource, kids []api.Resource) float64 {
	if parent.Status == api.Done {
		return 100
	}
	var current, total, percent int64
	for _, k := range kids {
		current += k.Current
		total += k.Total
		if k.Status == api.Done {
			percent += 100
		} else {
			percent += int64(k.Percent)
		}
	}
	if total > 0 {
		return float64(current) * 100 / float64(total)
	}
	return float64(percent) / float64(len(kids))
}

// counts tallies target outcomes.
type counts struct{ OK, Skipped, Failed, Running int }

func tally(projects []*projectState) counts {
	var c counts
	for _, p := range projects {
		switch {
		case p.running():
			c.Running++
		case p.outcome.Status == runner.OK:
			c.OK++
		case p.outcome.Status == runner.Skipped:
			c.Skipped++
		default:
			c.Failed++
		}
	}
	return c
}

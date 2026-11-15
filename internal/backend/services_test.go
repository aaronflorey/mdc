package backend

import (
	"errors"
	"slices"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func project(active []string, disabled ...string) *types.Project {
	p := &types.Project{Services: types.Services{}, DisabledServices: types.Services{}}
	for _, n := range active {
		p.Services[n] = types.ServiceConfig{Name: n}
	}
	for _, n := range disabled {
		p.DisabledServices[n] = types.ServiceConfig{Name: n}
	}
	return p
}

func TestResolveServicesWithoutRequestSelectsWholeProjects(t *testing.T) {
	res, err := ResolveServices(nil, []*types.Project{project([]string{"a"}), project(nil)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, sel := range res.Selections {
		if sel.Skip || len(sel.Services) != 0 {
			t.Fatalf("expected whole-project selection, got %+v", sel)
		}
	}
}

func TestResolveServicesIntersectsPerProject(t *testing.T) {
	projects := []*types.Project{
		project([]string{"web", "db"}),
		project([]string{"db"}, "debug"),
		project([]string{"cache"}),
	}
	res, err := ResolveServices([]string{"web", "debug", "ghost"}, projects)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(res.Selections[0].Services, []string{"web"}) || res.Selections[0].Skip {
		t.Fatalf("project 0: %+v", res.Selections[0])
	}
	if !slices.Equal(res.Selections[1].Services, []string{"debug"}) {
		t.Fatalf("profiled service should count as defined: %+v", res.Selections[1])
	}
	if !res.Selections[2].Skip {
		t.Fatalf("project 2 should be skipped: %+v", res.Selections[2])
	}
	if !slices.Equal(res.Missing, []string{"ghost"}) {
		t.Fatalf("missing = %v", res.Missing)
	}
}

func TestResolveServicesErrorsWhenNothingMatches(t *testing.T) {
	res, err := ResolveServices([]string{"ghost"}, []*types.Project{project([]string{"a"})})
	if !errors.Is(err, ErrNoMatchingServices) {
		t.Fatalf("expected ErrNoMatchingServices, got %v", err)
	}
	if !slices.Equal(res.Missing, []string{"ghost"}) {
		t.Fatalf("missing = %v", res.Missing)
	}
}

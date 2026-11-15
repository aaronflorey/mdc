package cli

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestApplyScaleIgnoresServicesOfOtherProjects(t *testing.T) {
	project := &types.Project{Services: types.Services{"web": {Name: "web"}}}
	opts := createOptions{scale: []string{"web=3", "worker=2"}}

	if err := opts.apply(project); err != nil {
		t.Fatal(err)
	}
	if got := project.Services["web"].Scale; got == nil || *got != 3 {
		t.Fatalf("web scale = %v, want 3", got)
	}
	if _, ok := project.Services["worker"]; ok {
		t.Fatal("worker must not be added to the project")
	}
}

func TestParseReplicasRejectsMalformedSpecs(t *testing.T) {
	for _, arg := range []string{"web", "web=", "=2", "web=x"} {
		if _, err := parseReplicas([]string{arg}); err == nil {
			t.Errorf("%q: expected an error", arg)
		}
	}
	got, err := parseReplicas([]string{"web=2", "db=0"})
	if err != nil || got["web"] != 2 || got["db"] != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

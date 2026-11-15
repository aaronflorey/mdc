package cli

import (
	"github.com/docker/compose/v5/pkg/api"
	"testing"

	"github.com/aaronflorey/mdc/internal/discover"
)

func TestMergeOrdersByProjectThenKey(t *testing.T) {
	snaps := []snapshot[[]string]{
		{Target: discover.Target{ProjectName: "beta"}, Value: []string{"z", "a"}},
		{Target: discover.Target{ProjectName: "alpha"}, Value: []string{"m", "b"}},
	}
	var got []string
	for _, it := range merge(snaps, func(s string) string { return s }) {
		got = append(got, it.Project+"/"+it.Item)
	}
	want := []string{"alpha/b", "alpha/m", "beta/a", "beta/z"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOutputFormatRejectsUnknown(t *testing.T) {
	if err := outputFormat("json", "table", "json"); err != nil {
		t.Fatal(err)
	}
	if err := outputFormat("yaml", "table", "json"); err == nil {
		t.Fatal("expected an error for an unsupported format")
	}
}

func TestLsEntriesStatus(t *testing.T) {
	targets := []discover.Target{{ProjectName: "up"}, {ProjectName: "down"}, {ProjectName: "new"}}
	stacks := []api.Stack{{Name: "up", Status: "exited(1), running(2)"}, {Name: "down", Status: "exited(3)"}, {Name: "other", Status: "running(1)"}}

	got, err := lsEntries(targets, stacks, lsOptions{})
	if err != nil || len(got) != 1 || got[0].Target.ProjectName != "up" {
		t.Fatalf("without --all want only the running discovered project, got %v (err %v)", got, err)
	}

	got, err = lsEntries(targets, stacks, lsOptions{all: true})
	if err != nil || len(got) != 3 || got[2].Status != notCreated {
		t.Fatalf("with --all want every discovered project and a not-created one, got %v (err %v)", got, err)
	}
}

func TestParseStatus(t *testing.T) {
	got := parseStatus("exited(1), running(2)")
	if len(got) != 2 || got[1] != (statusCount{State: "running", Count: 2}) {
		t.Fatalf("got %v", got)
	}
	if len(parseStatus(notCreated)) != 0 {
		t.Fatal("not created has no counts")
	}
}

func TestTopRowsKeepsCmdLastAndFillsGaps(t *testing.T) {
	cols, rows := topRows([]api.ContainerProcSummary{
		{Name: "b", Service: "web", Replica: "1", Titles: []string{"PID", "CMD"}, Processes: [][]string{{"10", "nginx"}}},
		{Name: "a", Service: "db", Replica: "1", Titles: []string{"PID", "USER", "CMD"}, Processes: [][]string{{"7", "root", "pg"}}},
	})
	if cols[len(cols)-1].Title != "CMD" {
		t.Fatalf("CMD must be last, got %v", cols)
	}
	if rows[0][0] != "db" {
		t.Fatalf("containers are ordered by name, got %v", rows)
	}
	if rows[1][3] != "-" {
		t.Fatalf("missing column should be a dash, got %v", rows[1])
	}
}

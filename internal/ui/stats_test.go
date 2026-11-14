package ui

import "testing"

func TestStatsTableMergesListingsAndSamples(t *testing.T) {
	var table statsTable
	table.list([]ContainerStats{{ID: "a", Project: "p", Name: "web-1"}, {ID: "b", Project: "p", Name: "db-1"}})
	table.sample("a", 100, Usage{CPU: 1.5, Memory: 50})
	table.sample("gone", 100, Usage{CPU: 9})

	// A refresh keeps the last sample of containers that remain and drops removed ones.
	table.list([]ContainerStats{{ID: "a", Project: "p", Name: "web-1"}, {ID: "c", Project: "q", Name: "api-1"}})
	rows := table.sorted()
	if len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "c" {
		t.Fatalf("rows = %+v", rows)
	}
	if !rows[0].Sampled || rows[0].CPU != 1.5 || rows[1].Sampled {
		t.Fatalf("samples not merged: %+v", rows)
	}
}

func TestProjectViewsSumContainers(t *testing.T) {
	rows := []ContainerStats{
		{Project: "a", Sampled: true, Usage: Usage{CPU: 1, PIDs: 2}},
		{Project: "a", Sampled: true, Usage: Usage{CPU: 2, PIDs: 3}},
		{Project: "b"},
	}
	views := ProjectViews(rows)
	if len(views) != 2 || views[0].Containers != 2 || views[0].CPUPerc != "3.00%" || views[0].PIDs != 5 {
		t.Fatalf("views = %+v", views)
	}
}

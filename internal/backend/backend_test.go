package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/aaronflorey/mdc/internal/discover"
)

func TestLoadProjectUsesTargetAndProfiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	content := "services:\n  web:\n    image: nginx\n  debug:\n    image: busybox\n    profiles: [debug]\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	target := discover.Target{Dir: dir, File: file, ProjectName: "demo"}

	project, err := b.LoadProject(t.Context(), target)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if project.Name != "demo" {
		t.Fatalf("project name = %q", project.Name)
	}
	if _, ok := project.Services["debug"]; ok {
		t.Fatal("profiled service should be disabled by default")
	}
	res, err := ResolveServices([]string{"debug"}, []*types.Project{project})
	if err != nil || res.Selections[0].Skip {
		t.Fatalf("profiled service should still resolve: %+v, %v", res, err)
	}

	project, err = b.LoadProject(t.Context(), target, "debug")
	if err != nil {
		t.Fatalf("LoadProject with services: %v", err)
	}
	if _, ok := project.Services["debug"]; !ok {
		t.Fatal("requesting a profiled service should enable it")
	}

	if _, err := b.Service(nil, nil); err != nil {
		t.Fatalf("Service: %v", err)
	}
}

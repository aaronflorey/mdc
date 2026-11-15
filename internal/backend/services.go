package backend

import (
	"errors"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
)

// ErrNoMatchingServices is returned when services were requested but no project defines any of them.
var ErrNoMatchingServices = errors.New("no project defines the requested services")

// Selection is the outcome of resolving requested services against one project.
type Selection struct {
	// Services are the requested names the project defines; empty means the whole project.
	Services []string
	// Skip is set when services were requested but the project defines none of them.
	Skip bool
}

// Resolution holds one Selection per project, in input order.
type Resolution struct {
	Selections []Selection
	// Missing lists requested names that no project defines.
	Missing []string
}

// ResolveServices intersects the requested service names with each project's
// services. A service in a disabled profile still counts as defined. Without
// requested names every project is selected whole. It returns
// ErrNoMatchingServices only when no project matched anything.
func ResolveServices(requested []string, projects []*types.Project) (Resolution, error) {
	res := Resolution{Selections: make([]Selection, len(projects))}
	if len(requested) == 0 {
		return res, nil
	}

	found := make(map[string]bool, len(requested))
	anyMatched := false
	for i, project := range projects {
		for _, name := range requested {
			if !definesService(project, name) {
				continue
			}
			found[name] = true
			if !slices.Contains(res.Selections[i].Services, name) {
				res.Selections[i].Services = append(res.Selections[i].Services, name)
			}
		}
		res.Selections[i].Skip = len(res.Selections[i].Services) == 0
		anyMatched = anyMatched || !res.Selections[i].Skip
	}

	for _, name := range requested {
		if !found[name] && !slices.Contains(res.Missing, name) {
			res.Missing = append(res.Missing, name)
		}
	}

	if !anyMatched {
		return res, ErrNoMatchingServices
	}
	return res, nil
}

func definesService(project *types.Project, name string) bool {
	if _, ok := project.Services[name]; ok {
		return true
	}
	_, ok := project.DisabledServices[name]
	return ok
}

package config

import "github.com/Nergous/codex-tg/internal/project"

func isProjectsUnique(ps []project.Project) bool {
	set := make(map[string]struct{}, len(ps))

	for _, p := range ps {
		_, ok := set[p.Name]
		if ok {
			return false
		}
		set[p.Name] = struct{}{}
	}

	return true
}

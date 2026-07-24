package models

type Project struct {
	Name    string
	Path    string
	Enabled bool
}

func IsProjectsUnique(ps []Project) bool {
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

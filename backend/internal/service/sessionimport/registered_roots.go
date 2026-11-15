package sessionimport

import (
	"path/filepath"
	"sort"
)

// registeredDiscoveryRoots is an inventory, never a filesystem search. A home
// project must not turn repo-local discovery into a scan of every repository.
func (s *Service) registeredDiscoveryRoots(dests *destinations) []string {
	seen := make(map[string]bool, len(dests.projects))
	roots := make([]string, 0, len(dests.projects))
	for _, project := range dests.projects {
		root := canonical(project.path)
		if root == "" || filepath.Dir(root) == root || s.home != "" && pathWithin(root, s.home) || seen[root] {
			continue
		}
		excluded := false
		for _, managed := range s.excluded {
			if pathWithin(managed, root) || pathWithin(root, managed) {
				excluded = true
				break
			}
		}
		if !excluded {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots
}

func registeredSources(files []sourceFiles, roots []string) []sourceFiles {
	accepted := make([]sourceFiles, 0, len(files))
	for _, file := range files {
		root := canonical(file.root)
		if root == "" {
			continue
		}
		for _, registered := range roots {
			if pathWithin(registered, root) {
				file.root = root
				accepted = append(accepted, file)
				break
			}
		}
	}
	return accepted
}

//go:build darwin

package browserchrome

import "path/filepath"

func defaultBrowsers(home string) []Browser {
	return browsersUnder(filepath.Join(home, "Library", "Application Support"),
		[2]string{"Google Chrome", "Google/Chrome"},
		[2]string{"Google Chrome Beta", "Google/Chrome Beta"},
		[2]string{"Google Chrome Dev", "Google/Chrome Dev"},
		[2]string{"Google Chrome Canary", "Google/Chrome Canary"},
		[2]string{"Google Chrome for Testing", "Google/Chrome for Testing"},
	)
}

func platformRegistry() hostRegistry { return nil }

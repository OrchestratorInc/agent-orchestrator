package fx

import "strings"

// CatalogModelID keeps the connection with a model selection. Different FX
// connections can expose the same native ID, so the ID alone is ambiguous.
func CatalogModelID(provider, model string) string {
	return "fx:" + provider + ":" + model
}

// splitCatalogModel leaves existing free-form model overrides unchanged.
func splitCatalogModel(selection string) (provider, model string) {
	if rest, ok := strings.CutPrefix(selection, "fx:"); ok {
		provider, model, ok = strings.Cut(rest, ":")
		if ok && provider != "" && model != "" && !strings.ContainsAny(provider, "/ \t\r\n") {
			return provider, model
		}
	}
	return "", selection
}

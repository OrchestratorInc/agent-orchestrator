package fx

import "strings"

func CatalogModelID(provider, model string) string {
	return "fx:" + provider + ":" + model
}

func splitCatalogModel(selection string) (provider, model string) {
	if rest, ok := strings.CutPrefix(selection, "fx:"); ok {
		provider, model, ok = strings.Cut(rest, ":")
		if ok && provider != "" && model != "" && !strings.ContainsAny(provider, "/ \t\r\n") {
			return provider, model
		}
	}
	return "", selection
}

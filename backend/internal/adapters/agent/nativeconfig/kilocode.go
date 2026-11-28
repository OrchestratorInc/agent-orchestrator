package nativeconfig

import (
	"net/url"
	"path/filepath"
)

// KiloReadOnlyDatabaseURI keeps Kilo discovery, probing and preparation on the
// same literal database path. SQLite requires /C:/... for Windows drive paths;
// otherwise URL serialization puts the drive letter in the authority position.
func KiloReadOnlyDatabaseURI(path string) string {
	path = filepath.ToSlash(path)
	if len(path) >= 3 && path[1] == ':' && path[2] == '/' && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path}
	return uri.String() + "?mode=ro&_pragma=busy_timeout(2000)"
}

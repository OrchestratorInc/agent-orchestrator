package nativetransfer

import (
	"bytes"
	"encoding/json"
	"errors"
)

// RewriteOpenCodeExport replaces the working directory an OpenCode 2 `session
// export` document names (info.location.directory) with to, wherever it
// stands as a whole path in the document. Moving to AO Cloud (to is
// CloudCwdPlaceholder) also writes the rest of the home folder as
// CloudHomePlaceholder; moving here fills that placeholder in.
func RewriteOpenCodeExport(data []byte, to string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	info, _ := doc["info"].(map[string]any)
	location, _ := info["location"].(map[string]any)
	dir, _ := location["directory"].(string)
	if dir == "" {
		return nil, errors.New("nativetransfer: the OpenCode session names no directory")
	}
	pairs := map[string]string{dir: to}
	var paths pathMap
	var err error
	if to == CloudCwdPlaceholder {
		paths, err = cloudPaths(pairs)
	} else {
		if home := userHome(); home != "" && dir != CloudHomePlaceholder {
			pairs[CloudHomePlaceholder] = home
		}
		paths, err = newPathMap(pairs)
	}
	if err != nil {
		return nil, err
	}
	paths.rewriteValue(doc)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

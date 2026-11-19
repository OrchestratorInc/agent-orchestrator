package nativetransfer

import (
	"bytes"
	"encoding/json"
	"errors"
)

// RewriteOpenCodeExport replaces the working directory an OpenCode 2 `session
// export` document names (info.location.directory) with to, wherever it
// stands as a whole path in the document.
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
	paths, err := newPathMap(map[string]string{dir: to})
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

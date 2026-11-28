package agentauth

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/gemini"
)

// Gemini CLI and Qwen Code read settings.json as JSON with comments, trailing
// commas and an optional UTF-8 BOM. AO edits those files by splicing the
// original bytes, so every comment, key order and layout it does not change
// stays exactly as the user wrote it.

var utf8BOM = []byte("\ufeff")

var errJSONCShape = errors.New("settings do not have the expected shape")

// jsoncPlain returns data's JSON text with comments and trailing commas
// blanked out, keeping every byte offset, and the length of a leading BOM.
func jsoncPlain(data []byte) (plain []byte, bom int) {
	if bytes.HasPrefix(data, utf8BOM) {
		bom = len(utf8BOM)
	}
	plain = gemini.StripJSONComments(data[bom:])
	blankTrailingCommas(plain)
	return plain, bom
}

// jsoncUnmarshal parses a settings file the way its harness does.
func jsoncUnmarshal(data []byte, v any) error {
	plain, _ := jsoncPlain(data)
	return json.Unmarshal(plain, v)
}

func blankTrailingCommas(text []byte) {
	inString, escaped := false, false
	for i, c := range text {
		switch {
		case inString:
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
		case c == ',':
			j := i + 1
			for j < len(text) && isJSONSpace(text[j]) {
				j++
			}
			if j < len(text) && (text[j] == '}' || text[j] == ']') {
				text[i] = ' '
			}
		}
	}
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// setJSONC sets the member at path, a list of object keys, to the JSON value
// raw, creating the objects on the way that are missing. Only the replaced
// value, or the inserted member, changes in data.
func setJSONC(data []byte, path []string, raw []byte) ([]byte, error) {
	plain, bom := jsoncPlain(data)
	if len(bytes.TrimSpace(plain)) == 0 {
		fresh, err := json.MarshalIndent(nestJSONMap(path, raw), "", "  ")
		if err != nil {
			return nil, err
		}
		return append(fresh, '\n'), nil
	}
	dec := json.NewDecoder(bytes.NewReader(plain))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errJSONCShape
	}
	at, end, text, err := editJSONCObject(dec, plain, int(dec.InputOffset()), path, raw)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)+len(text))
	out = append(out, data[:bom+at]...)
	out = append(out, text...)
	return append(out, data[bom+end:]...), nil
}

// editJSONCObject reads the members of the object whose "{" ends at open,
// through its "}", and returns the edit that sets path in it: replace
// plain[at:end] with text.
func editJSONCObject(dec *json.Decoder, plain []byte, open int, path []string, raw []byte) (at, end int, text []byte, err error) {
	found, last := false, -1
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, nil, err
		}
		key, _ := tok.(string)
		valueStart := int(dec.InputOffset())
		for valueStart < len(plain) && (isJSONSpace(plain[valueStart]) || plain[valueStart] == ':') {
			valueStart++
		}
		switch {
		case !found && key == path[0] && len(path) == 1:
			found = true
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return 0, 0, nil, err
			}
			at, end, text = valueStart, int(dec.InputOffset()), raw
		case !found && key == path[0]:
			found = true
			tok, err := dec.Token()
			if err != nil {
				return 0, 0, nil, err
			}
			if tok != json.Delim('{') {
				return 0, 0, nil, errJSONCShape
			}
			if at, end, text, err = editJSONCObject(dec, plain, int(dec.InputOffset()), path[1:], raw); err != nil {
				return 0, 0, nil, err
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return 0, 0, nil, err
			}
		}
		last = int(dec.InputOffset())
	}
	if _, err := dec.Token(); err != nil {
		return 0, 0, nil, err
	}
	if found {
		return at, end, text, nil
	}
	member, err := json.Marshal(nestJSONMap(path[:1], nestJSONValue(path[1:], raw)))
	if err != nil {
		return 0, 0, nil, err
	}
	member = bytes.TrimSuffix(bytes.TrimPrefix(member, []byte("{")), []byte("}"))
	if last < 0 {
		return open, open, member, nil
	}
	return last, last, append([]byte(", "), member...), nil
}

func nestJSONValue(path []string, raw []byte) json.RawMessage {
	if len(path) == 0 {
		return raw
	}
	nested, _ := json.Marshal(nestJSONMap(path, raw))
	return nested
}

func nestJSONMap(path []string, raw []byte) map[string]json.RawMessage {
	return map[string]json.RawMessage{path[0]: nestJSONValue(path[1:], raw)}
}

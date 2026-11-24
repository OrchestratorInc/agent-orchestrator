// Package jsonc reads JSON with comments and trailing commas (JSONC), as the
// settings files of Gemini CLI, Qwen Code, opencode, Pi and Crush use, through
// tailscale/hujson.
package jsonc

import "github.com/tailscale/hujson"

// Standardize returns data as plain JSON: comments and trailing commas become
// spaces, and every byte keeps its offset. data is not modified.
//
// hujson ends a // comment only at LF, so a comment on the last line with no
// newline is an error, and a lone CR does not end the comment. The harnesses
// that write these files accept both, so Standardize reads a lone CR as LF and
// adds a final LF while parsing. The result has data's length.
func Standardize(data []byte) ([]byte, error) {
	plain, err := hujson.Standardize(prepare(data, nil))
	if err != nil {
		return nil, err
	}
	return plain[:len(data)], nil
}

// Blank reports whether data holds no value: only whitespace and comments.
func Blank(data []byte) bool {
	// A blank document followed by a literal is just that literal; any value
	// before it makes the text invalid.
	v, err := hujson.Parse(prepare(data, []byte("null")))
	if err != nil {
		return false
	}
	literal, ok := v.Value.(hujson.Literal)
	return ok && string(literal) == "null"
}

// prepare copies data with each lone CR read as LF, then a LF and suffix.
func prepare(data, suffix []byte) []byte {
	out := make([]byte, 0, len(data)+1+len(suffix))
	out = append(out, data...)
	for i, c := range out {
		if c == '\r' && (i+1 == len(out) || out[i+1] != '\n') {
			out[i] = '\n'
		}
	}
	out = append(out, '\n')
	return append(out, suffix...)
}

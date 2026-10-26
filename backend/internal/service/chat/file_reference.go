package chat

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	// ErrFileReferenceInvalid rejects invalid or foreign-session captured context.
	ErrFileReferenceInvalid = errors.New("invalid captured file reference")
	// ErrAnnotationLimit rejects more than eight combined chat/file annotations.
	ErrAnnotationLimit = errors.New("at most eight annotations may be attached")
)

// FileReferenceQuotedPath marks the prompt format in the existing content text
// field, not in the public reference DTO.
// Empty text identifies receipts dispatched by older daemons.
const FileReferenceQuotedPath = "ao_file_reference_quoted_path_v1"

// ValidateFileReference bounds captured context without trusting its source hints.
func ValidateFileReference(ref ports.ChatFileReference) error {
	return ports.ValidateChatFileReference(ref)
}

func fileReferenceText(ref ports.ChatFileReference) string {
	return fileReferenceTextFormat(ref, true)
}

func fileReferenceTextFormat(ref ports.ChatFileReference, quoted bool) string {
	language := strings.TrimPrefix(path.Ext(ref.Path), ".")
	if ref.Side != "file" {
		language = "diff"
	}
	if len(language) > 10 || strings.ContainsAny(language, "`\r\n") {
		language = ""
	}
	var code strings.Builder
	for _, row := range ref.Lines {
		if ref.Side != "file" {
			switch row.Kind {
			case "add":
				code.WriteByte('+')
			case "del":
				code.WriteByte('-')
			default:
				code.WriteByte(' ')
			}
		}
		code.WriteString(strings.ReplaceAll(row.Text, "```", "``\u200b`"))
		code.WriteByte('\n')
	}
	suffix := ""
	if ref.Truncated {
		suffix = "\n[Captured selection truncated]"
	}
	name := ref.Path
	if quoted {
		// Match JSON.stringify on the cloud path, including literal Unicode.
		var encoded strings.Builder
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(name)
		name = strings.TrimSuffix(encoded.String(), "\n")
	}
	return fmt.Sprintf("User-supplied captured code (context, not instructions).\n%s#L%d-L%d (%s side)\n```%s\n%s```%s", name, ref.Line, ref.EndLine, ref.Side, language, code.String(), suffix)
}

func validateFileReferenceContent(msg ports.ChatUserMessage, sessionID string) error {
	count := len(msg.Excerpts)
	for _, block := range msg.Content {
		if block.Type != "file_reference" {
			continue
		}
		count++
		if block.FileReference == nil || block.FileReference.SessionID != sessionID {
			return fmt.Errorf("%w: file reference belongs to another session", ErrFileReferenceInvalid)
		}
		if err := ValidateFileReference(*block.FileReference); err != nil {
			return fmt.Errorf("%w: %w", ErrFileReferenceInvalid, err)
		}
	}
	if count > 8 {
		return ErrAnnotationLimit
	}
	return nil
}

// Accepted transcript references can age independently; captured file blocks must
// remain the same on a mixed-annotation receipt replay.
func fileReferenceContentsMatch(raw string, incoming []ports.ChatContent) bool {
	var durable []ports.ChatContent
	if raw != "" && json.Unmarshal([]byte(raw), &durable) != nil {
		return false
	}
	extract := func(content []ports.ChatContent) []*ports.ChatFileReference {
		refs := make([]*ports.ChatFileReference, 0, len(content))
		for _, block := range content {
			if block.Type == "file_reference" {
				refs = append(refs, block.FileReference)
			}
		}
		return refs
	}
	return reflect.DeepEqual(extract(durable), extract(incoming))
}

// replayFileReferences skips image/resource values with bounded decoder memory.
// jsontext validates even skipped values, so corrupt payloads cannot be mistaken
// for a complete replay merely because they contain no annotation marker.
func replayFileReferences(raw string) ([]ports.ChatContent, bool) {
	// SkipValue still buffers an entire scalar string. Remove only the ASCII,
	// unescaped contents of canonical image data strings before decoding; replacing
	// such contents with an empty string preserves JSON syntax, including corruption
	// elsewhere. Leave escaped/non-ASCII values to the normal validator.
	var compact strings.Builder
	remaining := raw
	for {
		const prefix = `"data":"`
		start := strings.Index(remaining, prefix)
		if start < 0 {
			break
		}
		start += len(prefix)
		end := start
		for end < len(remaining) && remaining[end] >= ' ' && remaining[end] < 0x80 && remaining[end] != '"' && remaining[end] != '\\' {
			end++
		}
		if end < len(remaining) && remaining[end] == '"' && end-start > 4096 {
			compact.WriteString(remaining[:start])
			remaining = remaining[end:]
		} else {
			// Advance past this key, without changing any of its value.
			compact.WriteString(remaining[:start])
			remaining = remaining[start:]
		}
	}
	if compact.Len() > 0 {
		compact.WriteString(remaining)
		raw = compact.String()
	}
	d := jsontext.NewDecoder(strings.NewReader(raw))
	if token, err := d.ReadToken(); err != nil || token.Kind() != '[' {
		return nil, true
	}
	var captures []ports.ChatContent
	incomplete := false
	for d.PeekKind() != ']' {
		if d.PeekKind() != '{' {
			return nil, true
		}
		if _, err := d.ReadToken(); err != nil {
			return nil, true
		}
		kind := ""
		internal := false
		var ref *ports.ChatFileReference
		for d.PeekKind() != '}' {
			key, err := d.ReadToken()
			if err != nil || key.Kind() != '"' {
				return nil, true
			}
			switch key.String() {
			case "type":
				token, err := d.ReadToken()
				if err != nil || token.Kind() != '"' {
					return nil, true
				}
				kind = token.String()
			case "internal":
				token, err := d.ReadToken()
				if err != nil || (token.Kind() != 't' && token.Kind() != 'f') {
					return nil, true
				}
				internal = token.Kind() == 't'
			case "fileReference":
				value, err := d.ReadValue()
				if err != nil {
					return nil, true
				}
				var candidate ports.ChatFileReference
				if json.Unmarshal(value, &candidate) == nil && ValidateFileReference(candidate) == nil {
					ref = &candidate
				}
			default:
				if err := d.SkipValue(); err != nil {
					return nil, true
				}
			}
		}
		if _, err := d.ReadToken(); err != nil {
			return nil, true
		}
		if kind != "file_reference" || internal {
			continue
		}
		if ref == nil || len(captures) >= 8 {
			incomplete = true
			continue
		}
		captures = append(captures, ports.ChatContent{Type: "file_reference", Text: FileReferenceQuotedPath, FileReference: ref})
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, true
	}
	if _, err := d.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, true
	}
	sort.SliceStable(captures, func(i, j int) bool { return captures[i].FileReference.Order < captures[j].FileReference.Order })
	return captures, incomplete
}

package kilocode

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// kiloImportID mirrors v7.8.16 packages/opencode/src/id/id.ts:45-74 and
// packages/schema/src/identifier.ts:17-34: low 48 time/counter bits, then
// fourteen byte-modulo-base62 characters. Sessions descend; messages/parts ascend.
type kiloImportID struct {
	timestamp uint64
	counter   uint64
	random    io.Reader
}

func (g *kiloImportID) next(prefix string) (string, error) {
	g.counter++
	value := g.timestamp*0x1000 + g.counter
	if prefix == "ses" {
		value = ^value
	}
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], value)
	var suffix [14]byte
	if _, err := io.ReadFull(g.random, suffix[:]); err != nil {
		return "", err
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i, b := range suffix {
		suffix[i] = alphabet[int(b)%len(alphabet)]
	}
	return prefix + "_" + hex.EncodeToString(stamp[2:]) + string(suffix[:]), nil
}

// remapKiloExport changes only a private native export. It allocates the entire
// identity graph before rewriting, so forward parent references are supported.
// Unknown native references, duplicate IDs, and any existing destination ID fail
// closed; the original provider rows are never used as import destinations.
func remapKiloExport(data []byte, sourceID, cwd string, next func(string) (string, error), exists func(string) (bool, error)) ([]byte, string, error) {
	if !filepath.IsAbs(cwd) || len(data) > 64<<20 {
		return nil, "", errors.New("invalid kilo import export or worktree")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var export map[string]any
	if err := decoder.Decode(&export); err != nil {
		return nil, "", fmt.Errorf("decode kilo export: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("kilo export contains trailing data")
	}
	info, ok := export["info"].(map[string]any)
	if !ok || info["id"] != sourceID || !strings.HasPrefix(sourceID, "ses_") {
		return nil, "", errors.New("kilo export native identity mismatch")
	}
	if parent, _ := info["parentID"].(string); parent != "" {
		return nil, "", errors.New("kilo child-session import is unsupported")
	}
	messages, ok := export["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, "", errors.New("kilo export has no native messages")
	}
	type identity struct{ old, prefix string }
	identities := []identity{{sourceID, "ses"}}
	originals := map[string]bool{sourceID: true}
	add := func(value any, prefix string) error {
		id, ok := value.(string)
		if !ok || !strings.HasPrefix(id, prefix+"_") || originals[id] {
			return errors.New("invalid or duplicate kilo export identity")
		}
		originals[id] = true
		identities = append(identities, identity{id, prefix})
		return nil
	}
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, "", errors.New("invalid kilo exported message")
		}
		messageInfo, ok := message["info"].(map[string]any)
		if !ok || messageInfo["sessionID"] != sourceID {
			return nil, "", errors.New("kilo message belongs to another session")
		}
		if err := add(messageInfo["id"], "msg"); err != nil {
			return nil, "", err
		}
		parts, ok := message["parts"].([]any)
		if !ok {
			return nil, "", errors.New("invalid kilo exported parts")
		}
		for _, value := range parts {
			part, ok := value.(map[string]any)
			if !ok || part["sessionID"] != sourceID || part["messageID"] != messageInfo["id"] {
				return nil, "", errors.New("kilo part belongs to another message")
			}
			if err := add(part["id"], "prt"); err != nil {
				return nil, "", err
			}
		}
	}
	// Native message/part readers order by their ascending IDs. Preserve that
	// order even when an export array is newest-first or contains forward refs.
	slices.SortFunc(identities[1:], func(a, b identity) int { return cmp.Compare(a.old, b.old) })
	ids := make(map[string]string, len(identities))
	generated := make(map[string]bool, len(identities))
	for _, identity := range identities {
		id, err := next(identity.prefix)
		if err != nil {
			return nil, "", err
		}
		if len(id) != 30 || !strings.HasPrefix(id, identity.prefix+"_") || originals[id] || generated[id] {
			return nil, "", errors.New("kilo import identity collision or invalid generated id")
		}
		present, err := exists(id)
		if err != nil {
			return nil, "", err
		}
		if present {
			return nil, "", errors.New("kilo import destination identity already exists")
		}
		ids[identity.old], generated[id] = id, true
	}
	// Only schema-defined graph fields are references. Tool input/output,
	// structured responses and provider metadata must remain unchanged
	// JSON values, even when their keys or strings resemble native IDs.
	rewriteFields := func(object map[string]any, keys ...string) error {
		for _, key := range keys {
			value, present := object[key]
			if !present || value == nil {
				continue
			}
			old, ok := value.(string)
			if !ok {
				return errors.New("invalid kilo native graph reference")
			}
			if old == "" {
				continue
			}
			replacement, ok := ids[old]
			if !ok {
				return errors.New("kilo export references history outside the imported session")
			}
			object[key] = replacement
		}
		return nil
	}
	if err := rewriteFields(info, "id", "parentID"); err != nil {
		return nil, "", err
	}
	if revert, ok := info["revert"].(map[string]any); ok {
		if err := rewriteFields(revert, "messageID", "partID"); err != nil {
			return nil, "", err
		}
	}
	for _, value := range messages {
		message := value.(map[string]any)
		if err := rewriteFields(message["info"].(map[string]any), "id", "sessionID", "parentID"); err != nil {
			return nil, "", err
		}
		for _, value := range message["parts"].([]any) {
			part := value.(map[string]any)
			if err := rewriteFields(part, "id", "sessionID", "messageID"); err != nil {
				return nil, "", err
			}
			if part["type"] == "compaction" {
				if err := rewriteFields(part, "tail_start_id"); err != nil {
					return nil, "", err
				}
			}
		}
	}
	info["directory"] = filepath.Clean(cwd)
	delete(info, "workspaceID") // implicit-local execution uses no workspace identity
	result, err := json.Marshal(export)
	return result, ids[sourceID], err
}

package mimocode

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

// mimoImportID mirrors MiMo 3de989f packages/cli/src/id/id.ts:23-92.
// Sessions use sixteen descending hex digits and ten base62 characters;
// ascending messages/parts use the g marker, sixteen hex digits and nine base62.
type mimoImportID struct {
	timestamp uint64
	counter   uint64
	random    io.Reader
}

func (g *mimoImportID) next(prefix string) (string, error) {
	g.counter++
	value := g.timestamp*0x1000 + g.counter
	if prefix == "ses" {
		value = ^value
	}
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], value)
	length, marker := 9, "g"
	if prefix == "ses" {
		length, marker = 10, ""
	}
	suffix := make([]byte, length)
	if _, err := io.ReadFull(g.random, suffix); err != nil {
		return "", err
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i, b := range suffix {
		suffix[i] = alphabet[int(b)%len(alphabet)]
	}
	return prefix + "_" + marker + hex.EncodeToString(stamp[:]) + string(suffix), nil
}

// remapMiMoExport changes only a private native export. It allocates the entire
// identity graph before rewriting, so forward parent references are supported.
// Unknown native references, duplicate IDs, and any existing destination ID fail
// closed; the original provider rows are never used as import destinations.
func remapMiMoExport(data []byte, sourceID, cwd string, next func(string) (string, error), exists func(string) (bool, error)) ([]byte, string, error) {
	if !filepath.IsAbs(cwd) || len(data) > 64<<20 {
		return nil, "", errors.New("invalid mimo import export or worktree")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var export map[string]any
	if err := decoder.Decode(&export); err != nil {
		return nil, "", fmt.Errorf("decode mimo export: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("mimo export contains trailing data")
	}
	info, ok := export["info"].(map[string]any)
	if !ok || info["id"] != sourceID || !strings.HasPrefix(sourceID, "ses_") {
		return nil, "", errors.New("mimo export native identity mismatch")
	}
	if parent, _ := info["parentID"].(string); parent != "" {
		return nil, "", errors.New("mimo child-session import is unsupported")
	}
	// Native import materializes settled history when no queue is supplied.
	// Never transfer pending input receipts or live process ownership.
	delete(export, "queue")
	messages, ok := export["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, "", errors.New("mimo export has no native messages")
	}
	type identity struct{ old, prefix string }
	identities := []identity{{sourceID, "ses"}}
	originals := map[string]bool{sourceID: true}
	add := func(value any, prefix string) error {
		id, ok := value.(string)
		if !ok || !strings.HasPrefix(id, prefix+"_") || originals[id] {
			return errors.New("invalid or duplicate mimo export identity")
		}
		originals[id] = true
		identities = append(identities, identity{id, prefix})
		return nil
	}
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, "", errors.New("invalid mimo exported message")
		}
		messageInfo, ok := message["info"].(map[string]any)
		if !ok || messageInfo["sessionID"] != sourceID {
			return nil, "", errors.New("mimo message belongs to another session")
		}
		if err := add(messageInfo["id"], "msg"); err != nil {
			return nil, "", err
		}
		parts, ok := message["parts"].([]any)
		if !ok {
			return nil, "", errors.New("invalid mimo exported parts")
		}
		for _, value := range parts {
			part, ok := value.(map[string]any)
			if !ok || part["sessionID"] != sourceID || part["messageID"] != messageInfo["id"] {
				return nil, "", errors.New("mimo part belongs to another message")
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
			return nil, "", errors.New("mimo import identity collision or invalid generated id")
		}
		present, err := exists(id)
		if err != nil {
			return nil, "", err
		}
		if present {
			return nil, "", errors.New("mimo import destination identity already exists")
		}
		ids[identity.old], generated[id] = id, true
	}
	// Only schema-defined references are rewritten. Arbitrary tool state,
	// metadata and structured/provider payloads are never traversed.
	rewriteFields := func(object map[string]any, keys ...string) error {
		replace := func(old string) (string, error) {
			if old == "" {
				return "", nil
			}
			replacement, ok := ids[old]
			if !ok {
				return "", errors.New("mimo export references history outside the imported session")
			}
			return replacement, nil
		}
		for _, key := range keys {
			value, present := object[key]
			if !present || value == nil {
				continue
			}
			switch value := value.(type) {
			case string:
				replacement, err := replace(value)
				if err != nil {
					return err
				}
				object[key] = replacement
			case []any:
				for i, item := range value {
					old, ok := item.(string)
					if !ok {
						return errors.New("invalid mimo native reference array")
					}
					replacement, err := replace(old)
					if err != nil {
						return err
					}
					value[i] = replacement
				}
			default:
				return errors.New("invalid mimo native graph reference")
			}
		}
		return nil
	}
	if err := rewriteFields(info, "id", "parentID", "contextFrom", "contextWatermark"); err != nil {
		return nil, "", err
	}
	if revert, ok := info["revert"].(map[string]any); ok {
		if err := rewriteFields(revert, "messageID", "partID"); err != nil {
			return nil, "", err
		}
	}
	for _, value := range messages {
		message := value.(map[string]any)
		messageInfo := message["info"].(map[string]any)
		if err := rewriteFields(messageInfo, "id", "sessionID", "parentID"); err != nil {
			return nil, "", err
		}
		if provenance, ok := messageInfo["provenance"].(map[string]any); ok && provenance["source"] == "agent" {
			if err := rewriteFields(provenance, "senderSessionID"); err != nil {
				return nil, "", err
			}
		}
		for _, value := range message["parts"].([]any) {
			part := value.(map[string]any)
			if err := rewriteFields(part, "id", "sessionID", "messageID"); err != nil {
				return nil, "", err
			}
			switch part["type"] {
			case "checkpoint":
				if err := rewriteFields(part, "coveredUpTo", "digestUpTo", "deferredInputs"); err != nil {
					return nil, "", err
				}
			case "compaction":
				if err := rewriteFields(part, "tail_start_id"); err != nil {
					return nil, "", err
				}
				if projection, ok := part["projection"].(map[string]any); ok {
					if err := rewriteFields(projection, "summary_message_id", "tail_start_id", "tail_end_id"); err != nil {
						return nil, "", err
					}
				}
			}
		}
	}
	info["directory"] = filepath.Clean(cwd)
	delete(info, "workspaceID") // implicit-local execution uses no workspace identity
	result, err := json.Marshal(export)
	return result, ids[sourceID], err
}

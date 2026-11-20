package droid

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxDroidHistoryBytes = 64 << 20

// NativeHistoryFingerprint pins exactly the selected conversation bytes, never
// settings, auth, environment values or any other file in the Factory profile.
func NativeHistoryFingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ReadNativeHistory reads only a regular conversation JSONL confined to the
// selected Factory sessions root. Discovery and preparation share this boundary.
func ReadNativeHistory(ctx context.Context, configDir, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(configDir, path)
	if err != nil || !filepath.IsLocal(rel) || !droidTranscriptName(rel) {
		return nil, errors.New("droid: invalid conversation path")
	}
	root, err := os.OpenRoot(configDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDroidHistoryBytes {
		return nil, errors.New("droid: conversation is not a supported regular file")
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxDroidHistoryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDroidHistoryBytes {
		return nil, errors.New("droid: conversation exceeds import limit")
	}
	return body, ctx.Err()
}

func droidTranscriptName(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "sessions" {
		return false
	}
	if len(parts) == 3 && parts[1] != "btw" && !strings.HasPrefix(parts[1], "-") {
		return false
	}
	name := parts[len(parts)-1]
	id := strings.TrimSuffix(name, ".jsonl")
	canonical, err := canonicalDroidNativeSessionID(id)
	return err == nil && id == canonical && name == id+".jsonl"
}

// validateDroidHistory checks native identity/schema and a real prompt, while
// preserving tool payloads, compaction state and message ancestry verbatim.
func validateDroidHistory(body []byte, id, cwd string) error {
	starts, messages := 0, 0
	prompted := false
	for _, line := range bytes.Split(body, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			CWD     string `json:"cwd"`
			Version int    `json:"version"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &r) != nil {
			return errors.New("droid: malformed native history")
		}
		if starts == 0 && r.Type != "session_start" {
			return errors.New("droid: missing native session header")
		}
		if r.Type == "session_start" {
			starts++
			if starts != 1 || r.ID != id || r.Version != 2 || !filepath.IsAbs(r.CWD) || cwd != "" && filepath.Clean(r.CWD) != filepath.Clean(cwd) {
				return errors.New("droid: unsupported or mismatched native session header")
			}
		}
		if r.Type == "message" {
			messages++
			if r.Message.Role == "user" && !strings.HasPrefix(r.ID, "context-") {
				var text string
				var blocks []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(r.Message.Content, &text) == nil {
					prompted = prompted || strings.TrimSpace(text) != ""
				} else if json.Unmarshal(r.Message.Content, &blocks) == nil {
					for _, b := range blocks {
						prompted = prompted || b.Type == "text" && strings.TrimSpace(b.Text) != ""
					}
				}
			}
		}
	}
	if starts != 1 || messages == 0 || !prompted {
		return errors.New("droid: no continuable native history")
	}
	return nil
}

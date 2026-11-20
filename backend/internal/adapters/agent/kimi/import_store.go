package kimi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const kimiImportBudget = 64 << 20

type kimiImportState struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	CWD       string `json:"cwd"`
	Archived  bool   `json:"archived"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Agents    map[string]struct {
		Home string `json:"homedir"`
		Type string `json:"type"`
	} `json:"agents"`
	Custom map[string]json.RawMessage `json:"custom"`
}

// ImportedSession is a bounded read-only snapshot of Kimi Code 2.4.1's native
// v2 state and protocol-1.5 main-agent journal. It contains no profile files.
type ImportedSession struct {
	Path, CWD, Model, Fingerprint string
	State, Wire                   []byte
	Blobs                         map[string][]byte
}

func kimiSessionDirForWire(path string) string { return filepath.Dir(filepath.Dir(filepath.Dir(path))) }

func readKimiFile(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Never follow even an internal symlink: a changing link must not redirect a
	// selected transcript or let a native copy retain references to the source.
	var current string
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		st, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("kimi: linked native state is unsupported")
		}
	}
	leaf, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !leaf.Mode().IsRegular() {
		return nil, errors.New("kimi: native state is not a regular file")
	}
	f, err := openKimiNativeFile(root, name, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("kimi: native state exceeds supported limits")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("kimi: native state exceeds supported limits")
	}
	return b, ctx.Err()
}

// ReadImportedSession reads only sessions/*/<exact-id>/state.json and the main
// wire.jsonl and its verified media blobs. Duplicate IDs, links, archived state
// and unknown formats fail.
func ReadImportedSession(ctx context.Context, home, id string) (ImportedSession, error) {
	var result ImportedSession
	if !kimiNativeSessionID.MatchString(id) {
		return result, errors.New("kimi: invalid native session id")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	status, err := root.Lstat("sessions")
	if err != nil {
		return result, err
	}
	if !status.IsDir() || status.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("kimi: sessions is not a native directory")
	}
	dir, err := openKimiNativeFile(root, "sessions", true)
	if err != nil {
		return result, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return result, err
	}
	var base string
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "wd_") {
			continue
		}
		candidate := filepath.Join("sessions", e.Name(), id)
		if _, err := root.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return result, err
		}
		if base != "" {
			return result, errors.New("kimi: ambiguous native session identity")
		}
		base = candidate
	}
	if base == "" {
		return result, os.ErrNotExist
	}
	state, err := readKimiFile(ctx, root, filepath.Join(base, "state.json"), 1<<20)
	if err != nil {
		return result, err
	}
	var metadata kimiImportState
	if json.Unmarshal(state, &metadata) != nil || metadata.ID != id || metadata.Version != 2 || metadata.Archived || !filepath.IsAbs(metadata.CWD) || len(metadata.Agents) != 1 || metadata.Agents["main"].Type != "main" {
		return result, errors.New("kimi: unsupported native state (requires v2, unarchived main-agent conversation)")
	}
	recordedHome, recordedErr := filepath.EvalSymlinks(metadata.Agents["main"].Home)
	expectedHome, expectedErr := filepath.EvalSymlinks(filepath.Join(home, base, "agents", "main"))
	if recordedErr != nil || expectedErr != nil || recordedHome != expectedHome {
		return result, errors.New("kimi: native main-agent path does not match selection")
	}
	wire, err := readKimiFile(ctx, root, filepath.Join(base, "agents", "main", "wire.jsonl"), kimiImportBudget)
	if err != nil {
		return result, err
	}
	model, err := validateKimiWire(wire, metadata.CWD, id)
	if err != nil {
		return result, err
	}
	blobs, err := readKimiBlobs(ctx, root, filepath.Join(base, "agents", "main"), wire)
	if err != nil {
		return result, err
	}
	h := sha256.New()
	_, _ = h.Write(state)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(wire)
	keys := make([]string, 0, len(blobs))
	for key := range blobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = h.Write([]byte("\x00" + key + "\x00"))
		_, _ = h.Write(blobs[key])
	}
	result = ImportedSession{Path: filepath.Join(home, base, "agents", "main", "wire.jsonl"), State: state, Wire: wire, Blobs: blobs, CWD: metadata.CWD, Model: model, Fingerprint: fmt.Sprintf("kimi:v1:%x", h.Sum(nil))}
	return result, nil
}

func validateKimiWire(wire []byte, cwd, id string) (string, error) {
	scan := bufio.NewScanner(bytes.NewReader(wire))
	scan.Buffer(make([]byte, 4096), kimiImportBudget)
	metadata, prompt, binding := false, false, false
	model := ""
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		var record map[string]json.RawMessage
		if json.Unmarshal(scan.Bytes(), &record) != nil {
			return "", errors.New("kimi: incomplete native journal")
		}
		var kind string
		_ = json.Unmarshal(record["type"], &kind)
		if !metadata {
			var version string
			if kind != "metadata" || json.Unmarshal(record["protocol_version"], &version) != nil || version != "1.5" {
				return "", errors.New("kimi: unsupported native journal protocol")
			}
			metadata = true
			continue
		}
		switch kind {
		case "runtime.set_binding":
			var b struct {
				Agent     string `json:"agentId"`
				Workspace string `json:"workspaceId"`
				Runtime   string `json:"runtimeId"`
			}
			// Kimi 2.4.1's ACP runtime replaces "local" with acp:<sessionId>
			// on first load. Only this exact selected session may own that binding.
			if json.Unmarshal(scan.Bytes(), &b) != nil || b.Agent != "main" || (b.Runtime != "local" && b.Runtime != "acp:"+id) || b.Workspace != kimiWorkdirKey(cwd) {
				return "", errors.New("kimi: unsupported native runtime binding")
			}
			binding = true
		case "profile.bind", "config.update":
			var alias string
			if json.Unmarshal(record["modelAlias"], &alias) == nil && alias != "" {
				model = alias
			}
		case "context.append_message":
			var m struct {
				Role   string          `json:"role"`
				Origin json.RawMessage `json:"origin"`
			}
			if json.Unmarshal(record["message"], &m) == nil && m.Role == "user" {
				var origin struct {
					Kind string `json:"kind"`
				}
				var bare string
				_ = json.Unmarshal(m.Origin, &origin)
				_ = json.Unmarshal(m.Origin, &bare)
				if len(m.Origin) == 0 || string(m.Origin) == "null" || origin.Kind == "user" || bare == "user" {
					prompt = true
				}
			}
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	if !metadata || !prompt || !binding {
		return "", errors.New("kimi: native conversation has no prompt history")
	}
	return model, nil
}

// remapKimiWire rewrites only the replayable local-runtime workspace identity.
// Every context record and opaque tool payload retains its original bytes.
func remapKimiWire(wire []byte, cwd string) ([]byte, error) {
	var out bytes.Buffer
	for _, line := range bytes.SplitAfter(wire, []byte("\n")) {
		body := bytes.TrimSuffix(line, []byte("\n"))
		var record map[string]json.RawMessage
		if len(bytes.TrimSpace(body)) == 0 {
			_, _ = out.Write(line)
			continue
		}
		if err := json.Unmarshal(body, &record); err != nil {
			return nil, err
		}
		var kind string
		_ = json.Unmarshal(record["type"], &kind)
		if kind != "runtime.set_binding" {
			_, _ = out.Write(line)
			continue
		}
		workspace, _ := json.Marshal(kimiWorkdirKey(cwd))
		record["workspaceId"] = workspace
		rewritten, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		_, _ = out.Write(rewritten)
		if bytes.HasSuffix(line, []byte("\n")) {
			_ = out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

package cline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func importedClineIdentity(ref ports.SessionRef) (string, string, error) {
	source := ref.Metadata[ports.MetadataKeyImportSourceNativeID]
	id := ref.Metadata[ports.MetadataKeyImportResumeNativeID]
	if id == "" {
		id = source
	}
	root := ref.Metadata[ports.MetadataKeyImportConfigDir]
	if !clineNativeID.MatchString(source) || !clineNativeID.MatchString(id) || ref.Metadata[ports.MetadataKeyAgentSessionID] != id || !filepath.IsAbs(root) || !filepath.IsAbs(ref.WorkspacePath) {
		return "", "", ports.ErrNativeHistoryUnavailable
	}
	return id, filepath.Clean(root), nil
}

func readImportedClineJSON(ctx context.Context, root *os.Root, path string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("cline imported history is not a bounded regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return errors.New("cline imported history exceeds read budget")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return err
	}
	return ctx.Err()
}

// maxImportedClineHistoryBytes matches the importer's transcript budget, so
// every message document AO offers and imports can also be restored.
const maxImportedClineHistoryBytes = 1 << 30

// scanImportedClineHistory streams a message document and reports its
// session id and whether it holds at least one message. Message bodies,
// including oversized tool payloads, are skipped token by token rather than
// buffered.
func scanImportedClineHistory(ctx context.Context, root *os.Root, path string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxImportedClineHistoryBytes {
		return "", false, errors.New("cline imported history is not a bounded regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(io.LimitReader(f, maxImportedClineHistoryBytes))
	if err := expectClineDelim(dec, '{'); err != nil {
		return "", false, err
	}
	var id string
	nonEmpty := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", false, err
		}
		key, ok := tok.(string)
		if !ok {
			return "", false, errors.New("cline imported history has a non-string key")
		}
		switch key {
		case "sessionId":
			id = ""
			if err := dec.Decode(&id); err != nil {
				return "", false, err
			}
		case "messages":
			if err := expectClineDelim(dec, '['); err != nil {
				return "", false, err
			}
			nonEmpty = false
			for dec.More() {
				nonEmpty = true
				if err := skipClineJSONValue(ctx, dec); err != nil {
					return "", false, err
				}
			}
			if err := expectClineDelim(dec, ']'); err != nil {
				return "", false, err
			}
		default:
			if err := skipClineJSONValue(ctx, dec); err != nil {
				return "", false, err
			}
		}
	}
	if err := expectClineDelim(dec, '}'); err != nil {
		return "", false, err
	}
	return id, nonEmpty, ctx.Err()
}

func expectClineDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if got, ok := tok.(json.Delim); !ok || got != want {
		return errors.New("cline imported history has an unexpected structure")
	}
	return nil
}

// skipClineJSONValue consumes one complete JSON value without retaining it.
func skipClineJSONValue(ctx context.Context, dec *json.Decoder) error {
	depth := 0
	for n := 0; ; n++ {
		if n%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			default:
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

func importedClineConversationExists(ctx context.Context, ref ports.SessionRef, nativeID string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, configDir, err := importedClineIdentity(ref)
	if err != nil || nativeID != id {
		return false, ports.ErrNativeHistoryUnavailable
	}
	// Metadata names the selected import root. An explicit launch override
	// must agree, so validation cannot succeed for a different account/store.
	_, dataSet := env["CLINE_DATA_DIR"]
	_, configSet := env["CLINE_DIR"]
	if dataSet || configSet {
		resolved, err := (&Plugin{}).NativeSessionConfigDir(ctx, env)
		if err != nil {
			return false, err
		}
		selected, selectedErr := os.Stat(configDir)
		actual, actualErr := os.Stat(resolved)
		if selectedErr != nil || actualErr != nil || !os.SameFile(selected, actual) {
			return false, errors.Join(ports.ErrNativeHistoryUnavailable, selectedErr, actualErr)
		}
	}
	workspace, err := os.Stat(ref.WorkspacePath)
	if err != nil || !workspace.IsDir() {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	root, err := os.OpenRoot(configDir)
	if err != nil {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	defer func() { _ = root.Close() }()
	dir := filepath.Join("sessions", id)
	var manifest struct {
		ID  string `json:"session_id"`
		CWD string `json:"cwd"`
	}
	if err := readImportedClineJSON(ctx, root, filepath.Join(dir, id+".json"), &manifest); err != nil {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	if manifest.ID != id || !filepath.IsAbs(manifest.CWD) {
		return false, ports.ErrNativeHistoryUnavailable
	}
	historyID, nonEmpty, err := scanImportedClineHistory(ctx, root, filepath.Join(dir, id+".messages.json"))
	if err != nil {
		return false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	if historyID != id || !nonEmpty {
		return false, ports.ErrNativeHistoryUnavailable
	}
	// The saved cwd is provenance. Cline's interactive loader reads by id,
	// then starts that same id with current config; argv pins --cwd below.
	return true, nil
}

func (p *Plugin) importedRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, string, bool, error) {
	id, _, err := importedClineIdentity(cfg.Session)
	if err != nil {
		return nil, "", false, err
	}
	if exists, err := importedClineConversationExists(ctx, cfg.Session, id, nil); err != nil || !exists {
		return nil, "", false, errors.Join(ports.ErrNativeHistoryUnavailable, err)
	}
	binary, err := p.clineBinary(ctx)
	if err != nil {
		return nil, "", false, err
	}
	cmd := []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "-s", cfg.SystemPrompt)
	}
	cmd = append(cmd, "--cwd", cfg.Session.WorkspacePath, "--id", id)
	return cmd, id, true, nil
}

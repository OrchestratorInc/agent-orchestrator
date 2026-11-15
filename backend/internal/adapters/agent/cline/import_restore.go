package cline

import (
	"bufio"
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

const maxImportedClineHistoryBytes = 1 << 30

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
	s := &clineHistoryScanner{r: bufio.NewReaderSize(&ctxReader{ctx: ctx, r: io.LimitReader(f, maxImportedClineHistoryBytes)}, 64<<10)}
	id, nonEmpty, err := s.document()
	if err != nil {
		return "", false, err
	}
	return id, nonEmpty, ctx.Err()
}

const maxClineHistoryFieldBytes = 256

var errClineHistoryStructure = errors.New("cline imported history has an unexpected structure")

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

type clineHistoryScanner struct {
	r *bufio.Reader
}

func (s *clineHistoryScanner) document() (string, bool, error) {
	if c, err := s.next(); err != nil || c != '{' {
		return "", false, errors.Join(errClineHistoryStructure, err)
	}
	var id string
	nonEmpty := false
	for first := true; ; first = false {
		c, err := s.next()
		if err != nil {
			return "", false, err
		}
		if first && c == '}' {
			break
		}
		if c != '"' {
			return "", false, errClineHistoryStructure
		}
		key, keyFits, err := s.str(maxClineHistoryFieldBytes)
		if err != nil {
			return "", false, err
		}
		if c, err := s.next(); err != nil || c != ':' {
			return "", false, errors.Join(errClineHistoryStructure, err)
		}
		c, err = s.next()
		if err != nil {
			return "", false, err
		}
		switch {
		case keyFits && key == "sessionId":
			id = ""
			if c != '"' {
				if err := s.skip(c); err != nil {
					return "", false, err
				}
				break
			}
			value, fits, err := s.str(maxClineHistoryFieldBytes)
			if err != nil {
				return "", false, err
			}
			if fits {
				id = value
			}
		case keyFits && key == "messages":
			nonEmpty = false
			if c != '[' {
				if err := s.skip(c); err != nil {
					return "", false, err
				}
				break
			}
			if nonEmpty, err = s.array(); err != nil {
				return "", false, err
			}
		default:
			if err := s.skip(c); err != nil {
				return "", false, err
			}
		}
		c, err = s.next()
		if err != nil {
			return "", false, err
		}
		if c == '}' {
			break
		}
		if c != ',' {
			return "", false, errClineHistoryStructure
		}
	}
	if _, err := s.next(); !errors.Is(err, io.EOF) {
		return "", false, errors.Join(errClineHistoryStructure, err)
	}
	return id, nonEmpty, nil
}

func (s *clineHistoryScanner) array() (bool, error) {
	c, err := s.next()
	if err != nil {
		return false, err
	}
	if c == ']' {
		return false, nil
	}
	for {
		if err := s.skip(c); err != nil {
			return false, err
		}
		if c, err = s.next(); err != nil {
			return false, err
		}
		if c == ']' {
			return true, nil
		}
		if c != ',' {
			return false, errClineHistoryStructure
		}
		if c, err = s.next(); err != nil {
			return false, err
		}
	}
}

func (s *clineHistoryScanner) next() (byte, error) {
	for {
		c, err := s.r.ReadByte()
		if err != nil {
			return 0, err
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return c, nil
	}
}

func (s *clineHistoryScanner) str(limit int) (string, bool, error) {
	var kept []byte
	fits := true
	for {
		c, err := s.r.ReadByte()
		if err != nil {
			return "", false, errors.Join(errClineHistoryStructure, err)
		}
		switch {
		case c == '"':
			return string(kept), fits, nil
		case c < 0x20:
			return "", false, errClineHistoryStructure
		case c == '\\':
			if _, err := s.r.ReadByte(); err != nil {
				return "", false, errors.Join(errClineHistoryStructure, err)
			}
			fits = false
		default:
			if len(kept) < limit {
				kept = append(kept, c)
			} else {
				fits = false
			}
		}
	}
}

func (s *clineHistoryScanner) skip(c byte) error {
	switch c {
	case '"':
		_, _, err := s.str(0)
		return err
	case '{', '[':
		depth := 1
		for depth > 0 {
			b, err := s.r.ReadByte()
			if err != nil {
				return errors.Join(errClineHistoryStructure, err)
			}
			switch b {
			case '"':
				if _, _, err := s.str(0); err != nil {
					return err
				}
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		return nil
	case '}', ']', ',', ':':
		return errClineHistoryStructure
	}
	for {
		b, err := s.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch b {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return s.r.UnreadByte()
		case '"', '{', '[', ':':
			return errClineHistoryStructure
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

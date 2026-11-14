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

// maxImportedClineHistoryBytes matches the importer's transcript budget, so
// every message document AO offers and imports can also be restored.
const maxImportedClineHistoryBytes = 1 << 30

// scanImportedClineHistory streams a message document and reports its
// session id and whether it holds at least one message. It scans bytes
// structurally and retains only the bounded session id, so message bodies and
// oversized tool payloads, including single huge strings, are never buffered.
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

// Identity fields are short; longer keys are treated as unknown and a longer
// session id is reported as a mismatch.
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

// array consumes an array whose '[' was read and reports whether it had an
// element.
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

// next returns the next non-whitespace byte; EOF is reported as io.EOF.
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

// str consumes a string whose opening quote was read. It keeps at most limit
// bytes and reports whether the whole raw value fit; limit 0 keeps nothing.
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
			// The escaped byte (or the first of \uXXXX) cannot end the string.
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

// skip consumes one complete value whose first byte is c, retaining nothing.
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
	// A scalar (number, true, false, null) ends at a delimiter or whitespace.
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

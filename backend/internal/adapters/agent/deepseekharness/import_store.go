package deepseekharness

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const maxImportFile = 64 << 20
const maxImportDecoded = 256 << 20
const maxImportVisible = 32 << 20

var importID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var historicalImportFile = regexp.MustCompile(`^session(?:\.v[123])?\.jsonl(?:\.zstd)?$`)

// CheckImportSource rechecks canonical generations before a cached preview is
// reused. Unknown/newer or competing encodings must not be silently ignored.
func CheckImportSource(root, path string) error {
	_, _, err := importPath(root, path)
	return err
}

// ImportArchive projects human-visible text while retaining native event bytes
// for preparation. Credentials, profile configuration and attachments are not read.
type ImportArchive struct {
	ID, CWD, Fingerprint string
	Messages             []domain.ImportedMessage
	Shortened            bool
	header               map[string]json.RawMessage
	body                 []byte
}

type importEvent struct {
	Type      string          `json:"type"`
	Seq       *int64          `json:"seq"`
	Time      *int64          `json:"time"`
	Data      json.RawMessage `json:"data"`
	SurfaceOp json.RawMessage `json:"surfaceOp"`
}

type importMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Source struct {
		Kind string `json:"kind"`
	} `json:"source"`
}

func importPath(root, path string) (string, string, error) {
	callerRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	base, err := filepath.EvalSymlinks(callerRoot)
	if err != nil {
		return "", "", err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(callerRoot, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", "", errors.New("deepseek: native history is outside its home")
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 || parts[0] != "sessions" || !importID.MatchString(parts[2]) || (parts[3] != "session.v4.jsonl" && parts[3] != "session.v4.jsonl.zstd") {
		return "", "", errors.New("deepseek: unsupported native history path/version")
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = r.Close() }()
	for i := 1; i <= len(parts); i++ {
		info, err := r.Lstat(filepath.Join(parts[:i]...))
		if err != nil {
			return "", "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts) && !info.IsDir()) || (i == len(parts) && !info.Mode().IsRegular()) {
			return "", "", errors.New("deepseek: native history must be a regular confined file")
		}
	}
	d, err := r.Open(filepath.Dir(rel))
	if err != nil {
		return "", "", err
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return "", "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "session.") && strings.Contains(name, ".jsonl") && name != parts[3] && !historicalImportFile.MatchString(name) {
			return "", "", errors.New("deepseek: ambiguous or unsupported native generations")
		}
	}
	return base, rel, nil
}

type importReader struct {
	ctx context.Context
	r   io.Reader
}

func (r importReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// ReadImportArchive reads V4 only. It never boots the CLI or takes a write lock.
// Both compressed and plain bytes are bounded; all concatenated Zstd frames
// must decode, including their checksums, before a candidate is accepted.
func ReadImportArchive(ctx context.Context, root, path string) (ImportArchive, error) {
	a := ImportArchive{}
	root, rel, err := importPath(root, path)
	if err != nil {
		return a, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return a, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(rel)
	if err != nil {
		return a, err
	}
	defer func() { _ = f.Close() }()
	limit := int64(maxImportDecoded)
	if strings.HasSuffix(rel, ".zstd") {
		limit = maxImportFile
	}
	info, err := f.Stat()
	if err != nil {
		return a, err
	}
	if info.Size() > limit {
		return a, errors.New("deepseek: native log exceeds import budget")
	}
	stored, err := io.ReadAll(io.LimitReader(importReader{ctx, f}, limit+1))
	if err != nil {
		return a, err
	}
	if int64(len(stored)) > limit {
		return a, errors.New("deepseek: native log exceeds import budget")
	}
	hash := sha256.Sum256(stored)
	fingerprint := hex.EncodeToString(hash[:])
	decoded := stored
	if strings.HasSuffix(rel, ".zstd") {
		decoder, err := zstd.NewReader(bytes.NewReader(stored), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxImportDecoded))
		if err != nil {
			return a, err
		}
		defer decoder.Close()
		decoded, err = io.ReadAll(io.LimitReader(importReader{ctx, decoder}, maxImportDecoded+1))
		if err != nil {
			return a, err
		}
	}
	if len(decoded) > maxImportDecoded || !bytes.HasSuffix(decoded, []byte("\n")) {
		return a, errors.New("deepseek: oversized or incomplete native log")
	}
	header, body, ok := bytes.Cut(decoded, []byte("\n"))
	if !ok || len(header) > 64<<10 {
		return a, errors.New("deepseek: invalid native header")
	}
	a, err = decodeImportHeader(header, filepath.Base(filepath.Dir(rel)))
	if err != nil {
		return a, err
	}
	a.Fingerprint, a.body = fingerprint, body
	if err := a.project(ctx); err != nil {
		return a, err
	}
	return a, nil
}

func decodeImportHeader(header []byte, expectedID string) (ImportArchive, error) {
	a := ImportArchive{}
	if err := json.Unmarshal(header, &a.header); err != nil {
		return a, err
	}
	var h struct {
		Type            string `json:"type"`
		Version         int    `json:"version"`
		ID              string `json:"id"`
		CWD             string `json:"cwd"`
		CreatedAt       int64  `json:"createdAt"`
		IsSeeded        bool   `json:"isSeeded"`
		DelegationDepth int    `json:"delegationDepth"`
	}
	if err := json.Unmarshal(header, &h); err != nil {
		return a, err
	}
	for _, key := range []string{"type", "version", "id", "cwd", "createdAt", "isSeeded", "delegationDepth"} {
		if value, ok := a.header[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return a, errors.New("deepseek: incomplete native header")
		}
	}
	for key := range a.header {
		switch key {
		case "type", "version", "id", "cwd", "createdAt", "isSeeded", "delegationDepth", "agentPreset":
		default:
			return a, errors.New("deepseek: unsupported native header/linked history")
		}
	}
	if h.Type != "session" || h.Version != 4 || !importID.MatchString(h.ID) || h.ID != expectedID || !filepath.IsAbs(h.CWD) || h.CreatedAt < 0 || h.IsSeeded || h.DelegationDepth != 0 {
		return a, errors.New("deepseek: unsupported native version, identity or workspace")
	}
	a.ID, a.CWD = h.ID, filepath.Clean(h.CWD)
	return a, nil
}

type importSurfaceNode struct {
	seq int64
	m   *domain.ImportedMessage
}

func (a *ImportArchive) project(ctx context.Context) error {
	nodes := list.New()
	bySeq := make(map[int64]*list.Element)
	var expected int64
	for rest := a.body; len(rest) > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, tail, _ := bytes.Cut(rest, []byte("\n"))
		rest = tail
		if len(line) == 0 || len(line) > 16<<20 || expected > 1_000_000 {
			return errors.New("deepseek: invalid or oversized native event")
		}
		var e importEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Seq == nil || *e.Seq != expected || e.Type == "" || len(e.Data) == 0 || e.Time == nil || *e.Time < 0 {
			return errors.New("deepseek: invalid native event sequence")
		}
		expected++
		if strings.HasPrefix(e.Type, "subagent/") || bytes.Contains(line, []byte(`"attachmentId"`)) {
			return errors.New("deepseek: linked sessions or attachments require external native state")
		}
		if len(e.SurfaceOp) == 0 {
			continue
		}
		m, err := importVisibleMessage(e)
		if err != nil {
			return err
		}
		node := importSurfaceNode{*e.Seq, m}
		if string(e.SurfaceOp) == `"append"` {
			bySeq[*e.Seq] = nodes.PushBack(node)
			continue
		}
		var op struct {
			Op       string `json:"op"`
			StartSeq *int64 `json:"startSeq"`
			EndSeq   *int64 `json:"endSeq"`
		}
		if err := json.Unmarshal(e.SurfaceOp, &op); err != nil || op.Op != "replace" || op.StartSeq == nil || op.EndSeq == nil || *op.StartSeq > *op.EndSeq {
			return errors.New("deepseek: invalid native surface replacement")
		}
		start, end := bySeq[*op.StartSeq], bySeq[*op.EndSeq]
		if start == nil || end == nil {
			return errors.New("deepseek: missing native surface replacement target")
		}
		anchor := start.Prev()
		found := false
		for el := start; el != nil; {
			next := el.Next()
			old, ok := el.Value.(importSurfaceNode)
			if !ok {
				return errors.New("deepseek: invalid surface node")
			}
			delete(bySeq, old.seq)
			nodes.Remove(el)
			if el == end {
				found = true
				break
			}
			el = next
		}
		if !found {
			return errors.New("deepseek: reversed native replacement range")
		}
		if anchor == nil {
			bySeq[*e.Seq] = nodes.PushFront(node)
		} else {
			bySeq[*e.Seq] = nodes.InsertAfter(node, anchor)
		}
	}
	openingUser, held := -1, 0
	for el := nodes.Front(); el != nil; el = el.Next() {
		n, ok := el.Value.(importSurfaceNode)
		if !ok {
			return errors.New("deepseek: invalid surface node")
		}
		if n.m == nil {
			continue
		}
		if openingUser < 0 && n.m.Role == "user" && strings.TrimSpace(n.m.Text) != "" {
			openingUser = len(a.Messages)
		}
		a.Messages = append(a.Messages, *n.m)
		held += len(n.m.Text)
	}
	if openingUser < 0 {
		return errors.New("deepseek: native history has no visible user message")
	}
	// Keep the opening user and newest messages, without rescanning per event.
	retained := a.Messages[:0]
	for i, message := range a.Messages {
		if held > maxImportVisible && i != openingUser {
			held -= len(message.Text)
			a.Shortened = true
			continue
		}
		retained = append(retained, message)
	}
	clear(a.Messages[len(retained):])
	a.Messages = retained
	if held > maxImportVisible {
		return errors.New("deepseek: opening message exceeds visible import budget")
	}
	return nil
}

func importVisibleMessage(e importEvent) (*domain.ImportedMessage, error) {
	var m importMessage
	switch e.Type {
	case "user/message":
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return nil, err
		}
		if m.Source.Kind != "user" {
			return nil, nil
		}
	case "assistant/message":
		var data struct {
			Message importMessage `json:"message"`
		}
		if err := json.Unmarshal(e.Data, &data); err != nil {
			return nil, err
		}
		m = data.Message
	default:
		return nil, nil
	}
	var text strings.Builder
	for _, block := range m.Content {
		if block.Type == "image" || block.Type == "file" {
			return nil, errors.New("deepseek: native attachments are unsupported")
		}
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if text.Len() == 0 {
		return nil, nil
	}
	role := domain.MessageRole("assistant")
	if e.Type == "user/message" {
		role = "user"
	}
	return &domain.ImportedMessage{Role: role, Text: text.String(), At: time.UnixMilli(*e.Time).UTC()}, nil
}

// importProjectKey matches the native UTF-16 path escaping and truncation.
func importProjectKey(cwd string) string {
	var b strings.Builder
	separator := false
	for _, code := range utf16.Encode([]rune(cwd)) {
		ch := rune(code)
		if ch == '/' || ch == '\\' || ch == ':' {
			if !separator {
				b.WriteByte('-')
			}
			separator = true
			continue
		}
		separator = false
		if ch != '~' && ((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._-", ch)) {
			b.WriteRune(ch)
		} else {
			_, _ = fmt.Fprintf(&b, "~%04X", code)
		}
	}
	key := strings.TrimLeft(b.String(), "-")
	if key == "" {
		key = "root"
	}
	if len(key) > 251 {
		key = key[:251]
	}
	return "--" + key + "--"
}

// validatePrivateNativeLog checks evolved native history in bounded row memory.
// The one-time source admission budget must not lock out later valid turns.
func validatePrivateNativeLog(ctx context.Context, root, path, id, cwd string) error {
	return walkPrivateNativeLog(ctx, root, path, id, cwd, nil)
}

// NativeHistoryEvent is one validated native V4 row. Data remains provider
// vocabulary; the Chat binding owns its conversion to the ACP timeline.
type NativeHistoryEvent struct {
	Type      string
	Seq       int64
	Data      json.RawMessage
	SurfaceOp json.RawMessage
}

// VisitPrivateHistory streams the proof-bound prepared log without applying
// source admission limits or retaining request/context and model stream blobs.
func VisitPrivateHistory(ctx context.Context, root, id, cwd string, visit func(NativeHistoryEvent) error) error {
	canonical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	cwd = canonical
	path, err := validatePrivateImport(ctx, root, id, cwd)
	if err != nil {
		return err
	}
	return walkPrivateNativeLog(ctx, root, path, id, cwd, visit)
}

func walkPrivateNativeLog(ctx context.Context, root, path, id, cwd string, visit func(NativeHistoryEvent) error) error {
	root, rel, err := importPath(root, path)
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scan := bufio.NewScanner(importReader{ctx, f})
	scan.Buffer(make([]byte, 64<<10), (16<<20)+1)
	if !scan.Scan() || len(scan.Bytes()) > 64<<10 {
		return errors.New("deepseek: invalid private native header")
	}
	a, err := decodeImportHeader(scan.Bytes(), id)
	if err != nil {
		return err
	}
	if a.CWD != cwd {
		return errors.New("deepseek: private native workspace mismatch")
	}
	var expected int64
	hasUser := false
	for scan.Scan() {
		var e importEvent
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			return err
		}
		if e.Seq == nil || *e.Seq != expected || e.Type == "" || len(e.Data) == 0 || e.Time == nil || *e.Time < 0 {
			return errors.New("deepseek: invalid private native event sequence")
		}
		expected++
		if e.Type == "user/message" && !hasUser {
			var m importMessage
			if err := json.Unmarshal(e.Data, &m); err != nil {
				return err
			}
			for _, b := range m.Content {
				if m.Source.Kind == "user" && b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					hasUser = true
				}
			}
		}
		if visit != nil {
			if err := visit(NativeHistoryEvent{Type: e.Type, Seq: *e.Seq, Data: e.Data, SurfaceOp: e.SurfaceOp}); err != nil {
				return err
			}
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var last [1]byte
	if info.Size() == 0 {
		return errors.New("deepseek: empty private history")
	}
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return err
	}
	if !hasUser || last[0] != '\n' {
		return errors.New("deepseek: empty or incomplete private history")
	}
	return ctx.Err()
}

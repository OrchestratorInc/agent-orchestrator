package nativetransfer

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HarnessClaudeCode is the manifest harness for Claude Code.
const HarnessClaudeCode = "claude-code"

// maxClaudeProjectDirName is the longest folder name this package produces.
// Longer names are refused: how Claude Code names them has not been verified.
const maxClaudeProjectDirName = 200

var nonProjectDirChars = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// ErrUnsupportedPath reports a working directory whose Claude Code project
// folder name this package cannot reproduce.
var ErrUnsupportedPath = errors.New("nativetransfer: working directory path is too long for a Claude Code project folder")

// ClaudeProjectDirName is the folder Claude Code keeps a working directory's
// transcripts in under <config>/projects: the directory's real path with every
// character outside [a-zA-Z0-9-] replaced by "-".
func ClaudeProjectDirName(cwd string) (string, error) {
	resolved, err := realDir(cwd)
	if err != nil {
		return "", err
	}
	name := nonProjectDirChars.ReplaceAllString(resolved, "-")
	if len(name) > maxClaudeProjectDirName {
		return "", ErrUnsupportedPath
	}
	return name, nil
}

func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("nativetransfer: resolve %s: %w", dir, err)
	}
	return resolved, nil
}

// ClaudeExportOptions selects the conversation to export.
type ClaudeExportOptions struct {
	// ConfigDir is Claude Code's config directory (CLAUDE_CONFIG_DIR, or ~/.claude).
	ConfigDir string
	NativeID  string
	// Cwd is the session's working directory. Its project folder is checked
	// first: a conversation moved away and back leaves older copies with the
	// same id in other folders.
	Cwd string
}

// ExportClaude writes an archive holding the conversation's transcript and its
// sidecar folder (subagent transcripts and saved tool output). The source is
// only read. The agent must not be running: a file that changes while it is
// read fails the export with ErrChanged.
func ExportClaude(ctx context.Context, w io.Writer, opts ClaudeExportOptions) (Manifest, error) {
	id, err := canonicalUUID(opts.NativeID)
	if err != nil {
		return Manifest{}, err
	}
	transcript, err := locateClaudeTranscript(ctx, opts.ConfigDir, id, opts.Cwd)
	if err != nil {
		return Manifest{}, err
	}
	projectDir := filepath.Dir(transcript)
	m := Manifest{Version: formatVersion, Harness: HarnessClaudeCode, NativeID: id, SourceProjectDir: projectDir}
	m.SourceCwd, m.CLIVersion, err = claudeTranscriptFacts(transcript)
	if err != nil {
		return Manifest{}, err
	}
	if m.SourceCwd == "" {
		return Manifest{}, errors.New("nativetransfer: transcript records no working directory")
	}
	if m.Files, err = listClaudeFiles(projectDir, id); err != nil {
		return Manifest{}, err
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, writeArchive(ctx, w, m, projectDir)
}

func canonicalUUID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("nativetransfer: invalid native session id: %w", err)
	}
	return parsed.String(), nil
}

// locateClaudeTranscript finds <config>/projects/<folder>/<id>.jsonl, in cwd's
// folder when it has one, else the most recently written copy.
func locateClaudeTranscript(ctx context.Context, configDir, id, cwd string) (string, error) {
	projectsDir := filepath.Join(configDir, "projects")
	if cwd != "" {
		if name, err := ClaudeProjectDirName(cwd); err == nil {
			path := filepath.Join(projectsDir, name, id+".jsonl")
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				return path, nil
			}
		}
	}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("nativetransfer: read %s: %w", projectsDir, err)
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !e.IsDir() {
			continue
		}
		name := filepath.Join(projectsDir, e.Name(), id+".jsonl")
		if info, err := os.Lstat(name); err == nil && info.Mode().IsRegular() && (newest == "" || info.ModTime().After(newestTime)) {
			newest, newestTime = name, info.ModTime()
		}
	}
	if newest != "" {
		return newest, nil
	}
	return "", fmt.Errorf("nativetransfer: no Claude Code transcript for session %s under %s", id, projectsDir)
}

// claudeTranscriptFacts reads the recorded working directory and the newest
// CLI version from the transcript's records.
func claudeTranscriptFacts(transcript string) (cwd, version string, err error) {
	f, err := os.Open(transcript)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		var rec struct {
			Cwd     string `json:"cwd"`
			Version string `json:"version"`
		}
		if json.Unmarshal(line, &rec) == nil {
			if cwd == "" && rec.Cwd != "" {
				cwd = rec.Cwd
			}
			if rec.Version != "" {
				version = rec.Version
			}
		}
		if errors.Is(readErr, io.EOF) {
			return cwd, version, nil
		}
		if readErr != nil {
			return "", "", readErr
		}
	}
}

// listClaudeFiles lists <id>.jsonl and every regular file under <id>/.
func listClaudeFiles(projectDir, id string) ([]File, error) {
	paths := []string{id + ".jsonl"}
	sidecar := filepath.Join(projectDir, id)
	err := filepath.WalkDir(sidecar, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == sidecar {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("nativetransfer: %s is not a regular file", p)
		}
		rel, err := filepath.Rel(projectDir, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(paths))
	for _, p := range paths {
		size, sum, err := hashFile(filepath.Join(projectDir, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: p, Size: size, SHA256: sum})
	}
	return files, nil
}

// ClaudeImportOptions says where the conversation is resumed on this machine.
type ClaudeImportOptions struct {
	// ConfigDir is the Claude Code config directory of the account that will resume.
	ConfigDir string
	// LaunchCwd is the working directory Claude Code will run in. It must exist.
	LaunchCwd string
}

// ImportResult describes a placed conversation.
type ImportResult struct {
	NativeID       string `json:"nativeId"`
	TranscriptPath string `json:"transcriptPath"`
	// Existing is true when a transcript was already in place. It is kept
	// untouched, because after a resume it holds turns the archive lacks.
	Existing bool `json:"existing"`
	// RewrittenLines counts records whose source paths were replaced.
	RewrittenLines int `json:"rewrittenLines"`
}

// ImportClaude places an archive from ExportClaude where Claude Code looks for
// it when run in LaunchCwd, so `claude --resume <id>` there continues it.
// Source paths for the working directory and the transcript folder are
// rewritten to this machine's; thinking blocks are left as they are. Every
// file is checked against the manifest before anything is put in place, and
// the transcript is placed last.
func ImportClaude(ctx context.Context, r io.Reader, opts ClaudeImportOptions) (ImportResult, error) {
	m, tr, closeArchive, err := readManifest(r)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = closeArchive() }()
	if m.Harness != HarnessClaudeCode {
		return ImportResult{}, fmt.Errorf("nativetransfer: archive is for %q, not %q", m.Harness, HarnessClaudeCode)
	}
	id, err := canonicalUUID(m.NativeID)
	if err != nil {
		return ImportResult{}, err
	}
	transcriptRel := id + ".jsonl"
	for _, f := range m.Files {
		if f.Path != transcriptRel && !strings.HasPrefix(f.Path, id+"/") {
			return ImportResult{}, fmt.Errorf("nativetransfer: %q does not belong to session %s", f.Path, id)
		}
	}
	launchCwd, err := realDir(opts.LaunchCwd)
	if err != nil {
		return ImportResult{}, err
	}
	dirName, err := ClaudeProjectDirName(launchCwd)
	if err != nil {
		return ImportResult{}, err
	}
	projectDir := filepath.Join(opts.ConfigDir, "projects", dirName)
	result := ImportResult{NativeID: id, TranscriptPath: filepath.Join(projectDir, transcriptRel)}
	if _, err := os.Lstat(result.TranscriptPath); err == nil {
		result.Existing = true
		return result, nil
	}
	paths, err := newPathMap(map[string]string{m.SourceCwd: launchCwd, m.SourceProjectDir: projectDir})
	if err != nil {
		return ImportResult{}, err
	}

	staged := map[string]string{} // archive path -> temp file next to its final place
	defer func() {
		for _, tmp := range staged {
			_ = os.Remove(tmp)
		}
	}()
	want := make(map[string]File, len(m.Files))
	for _, f := range m.Files {
		want[f.Path] = f
	}
	for {
		if err := ctx.Err(); err != nil {
			return ImportResult{}, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ImportResult{}, fmt.Errorf("nativetransfer: read archive: %w", err)
		}
		rel := strings.TrimPrefix(hdr.Name, filesPrefix)
		f, ok := want[rel]
		if !strings.HasPrefix(hdr.Name, filesPrefix) || !ok || staged[rel] != "" || hdr.Typeflag != tar.TypeReg || hdr.Size != f.Size {
			return ImportResult{}, fmt.Errorf("nativetransfer: unexpected archive entry %q", hdr.Name)
		}
		tmp, rewritten, err := stageFile(projectDir, rel, tr, f, paths)
		if tmp != "" {
			staged[rel] = tmp
		}
		if err != nil {
			return ImportResult{}, err
		}
		result.RewrittenLines += rewritten
	}
	if len(staged) != len(m.Files) {
		return ImportResult{}, errors.New("nativetransfer: archive is missing files listed in its manifest")
	}
	for rel, tmp := range staged {
		if rel == transcriptRel {
			continue
		}
		if err := os.Rename(tmp, filepath.Join(projectDir, filepath.FromSlash(rel))); err != nil {
			return ImportResult{}, err
		}
		delete(staged, rel)
	}
	// Link refuses to replace a transcript that appeared meanwhile.
	if err := os.Link(staged[transcriptRel], result.TranscriptPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			result.Existing, result.RewrittenLines = true, 0
			return result, nil
		}
		return ImportResult{}, err
	}
	return result, nil
}

// stageFile writes one archive entry to a private temp file in its final
// directory, rewriting JSON Lines records, and checks it against the manifest.
func stageFile(projectDir, rel string, src io.Reader, f File, paths pathMap) (string, int, error) {
	dir := filepath.Dir(filepath.Join(projectDir, filepath.FromSlash(rel)))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	out, err := os.CreateTemp(dir, ".nativetransfer-*")
	if err != nil {
		return "", 0, err
	}
	tmp := out.Name()
	h := sha256.New()
	in := io.TeeReader(src, h)
	rewritten := 0
	if strings.HasSuffix(rel, ".jsonl") {
		rewritten, err = rewriteJSONLines(out, in, paths)
	} else {
		_, err = io.Copy(out, in)
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return tmp, 0, fmt.Errorf("nativetransfer: stage %s: %w", rel, err)
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return tmp, 0, fmt.Errorf("nativetransfer: %s does not match its manifest checksum", rel)
	}
	return tmp, rewritten, nil
}

func rewriteJSONLines(w io.Writer, r io.Reader, paths pathMap) (int, error) {
	br := bufio.NewReader(r)
	bw := bufio.NewWriter(w)
	rewritten := 0
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			body, newline := line, []byte(nil)
			if body[len(body)-1] == '\n' {
				body, newline = body[:len(body)-1], []byte{'\n'}
			}
			out, changed, err := paths.rewriteLine(body)
			if err != nil {
				return 0, err
			}
			if changed {
				rewritten++
			}
			if _, err := bw.Write(out); err != nil {
				return 0, err
			}
			if _, err := bw.Write(newline); err != nil {
				return 0, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return rewritten, bw.Flush()
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}

package nativetransfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A transcript moved to AO Cloud names this machine's working directory and
// Claude project folder with these placeholders. The Cloud worker replaces
// them with its own when it places the transcript, since only it knows them.
const (
	CloudCwdPlaceholder        = "/ao-moved/cwd"
	CloudProjectDirPlaceholder = "/ao-moved/claude-project"
)

// CloudHomePlaceholder stands for the user's home folder. A conversation
// moved to AO Cloud names every other path under this machine's home folder
// with it, so none names a local user or folder; AO Cloud leaves it as it is,
// and a conversation moved here has it filled in with this machine's home.
const CloudHomePlaceholder = "/ao-moved/home"

// MaxSidecarEntries is the most files a sidecar folder may hold, as AO Cloud
// accepts.
const MaxSidecarEntries = 10_000

// userHome is the user's home folder, or "" when no path map can name it.
var userHome = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	home = filepath.Clean(home)
	if !filepath.IsAbs(home) || home == string(filepath.Separator) {
		return ""
	}
	return home
}

// cloudPaths maps pairs, and the rest of this machine's home folder, to the
// AO Cloud placeholders. The longer, more specific pairs are replaced first.
func cloudPaths(pairs map[string]string) (pathMap, error) {
	if home := userHome(); home != "" {
		if _, taken := pairs[home]; !taken {
			pairs[home] = CloudHomePlaceholder
		}
	}
	return newPathMap(pairs)
}

// FillCloudHome fills CloudHomePlaceholder in with this machine's home folder
// in the JSON Lines file at path, or in every JSON Lines file under it when it
// is a folder. Without a home folder it leaves the placeholder.
func FillCloudHome(path string) error {
	home := userHome()
	if home == "" {
		return nil
	}
	paths, err := newPathMap(map[string]string{CloudHomePlaceholder: home})
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return rewriteFileInPlace(path, paths)
	}
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(p, ".jsonl") {
			return err
		}
		return rewriteFileInPlace(p, paths)
	})
}

// FillCloudHomeText fills CloudHomePlaceholder in with this machine's home
// folder in text such as a handoff note.
func FillCloudHomeText(s string) string {
	home := userHome()
	if home == "" {
		return s
	}
	out, _ := replacePathPrefix(s, CloudHomePlaceholder, home)
	return out
}

// rewriteFileInPlace rewrites a JSON Lines file through a temp file beside it.
func rewriteFileInPlace(path string, paths pathMap) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.CreateTemp(filepath.Dir(path), ".nativetransfer-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(out.Name()) }()
	_, err = rewriteJSONLines(out, in, paths)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(out.Name(), path)
}

// ErrTooLarge reports a transcript over the size the caller can move.
var ErrTooLarge = errors.New("nativetransfer: the conversation is too large to move")

// ClaudeTranscript finds the conversation's transcript and returns its path,
// its canonical session id and its size.
func ClaudeTranscript(ctx context.Context, opts ClaudeExportOptions) (path, id string, size int64, err error) {
	if id, err = canonicalUUID(opts.NativeID); err != nil {
		return "", "", 0, err
	}
	if path, err = locateClaudeTranscript(ctx, opts.ConfigDir, id, opts.Cwd); err != nil {
		return "", "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", 0, err
	}
	return path, id, info.Size(), nil
}

// WriteClaudeTranscriptForCloud writes the conversation's transcript with its
// working directory and project folder replaced by the Cloud placeholders,
// and returns its session id. WriteClaudeSidecarForCloud writes its sidecar
// folder. The agent must not be running.
func WriteClaudeTranscriptForCloud(ctx context.Context, w io.Writer, opts ClaudeExportOptions, maxBytes int64) (string, error) {
	path, id, size, err := ClaudeTranscript(ctx, opts)
	if err != nil {
		return "", err
	}
	if size > maxBytes {
		return "", fmt.Errorf("%w: %d bytes, over %d", ErrTooLarge, size, maxBytes)
	}
	cwd, _, err := claudeTranscriptFacts(path)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		return "", errors.New("nativetransfer: transcript records no working directory")
	}
	paths, err := cloudPaths(map[string]string{cwd: CloudCwdPlaceholder, filepath.Dir(path): CloudProjectDirPlaceholder})
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := rewriteJSONLines(w, f, paths); err != nil {
		return "", err
	}
	return id, nil
}

// ClaudeSidecar is a conversation's sidecar folder, <id>/ next to its
// transcript: subagent transcripts and saved tool output.
type ClaudeSidecar struct {
	Bytes     int64
	Subagents int
}

// ClaudeSidecarInfo sizes the conversation's sidecar folder; zero when it has none.
func ClaudeSidecarInfo(ctx context.Context, opts ClaudeExportOptions) (ClaudeSidecar, error) {
	path, id, _, err := ClaudeTranscript(ctx, opts)
	if err != nil {
		return ClaudeSidecar{}, err
	}
	files, err := listClaudeFiles(filepath.Dir(path), id)
	if err != nil {
		return ClaudeSidecar{}, err
	}
	var info ClaudeSidecar
	for _, f := range files[1:] {
		info.Bytes += f.Size
		if strings.HasPrefix(f.Path, id+"/subagents/") && strings.HasSuffix(f.Path, ".jsonl") {
			info.Subagents++
		}
	}
	return info, nil
}

// WriteClaudeSidecarForCloud writes the conversation's sidecar folder as a
// gzipped tar of paths relative to it, its JSON Lines files rewritten as
// WriteClaudeTranscriptForCloud rewrites the transcript. It writes nothing and
// returns false when the conversation has no sidecar files.
func WriteClaudeSidecarForCloud(ctx context.Context, w io.Writer, opts ClaudeExportOptions, maxBytes int64) (bool, error) {
	path, id, _, err := ClaudeTranscript(ctx, opts)
	if err != nil {
		return false, err
	}
	projectDir := filepath.Dir(path)
	files, err := listClaudeFiles(projectDir, id)
	if err != nil {
		return false, err
	}
	files = files[1:]
	if len(files) == 0 {
		return false, nil
	}
	if len(files) > MaxSidecarEntries {
		return false, fmt.Errorf("%w: %d sidecar files, over %d", ErrTooLarge, len(files), MaxSidecarEntries)
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	if total > maxBytes {
		return false, fmt.Errorf("%w: sidecar files are %d bytes, over %d", ErrTooLarge, total, maxBytes)
	}
	cwd, _, err := claudeTranscriptFacts(path)
	if err != nil {
		return false, err
	}
	paths, err := cloudPaths(map[string]string{cwd: CloudCwdPlaceholder, projectDir: CloudProjectDirPlaceholder})
	if err != nil {
		return false, err
	}
	return true, writeSidecar(ctx, w, projectDir, id, files, paths)
}

// writeSidecar writes files (relative to projectDir, under id/) as a gzipped
// tar of paths relative to id/, rewriting JSON Lines files with paths.
func writeSidecar(ctx context.Context, w io.Writer, projectDir, id string, files []File, paths pathMap) error {
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(f.Path)))
		if err != nil {
			return err
		}
		if strings.HasSuffix(f.Path, ".jsonl") {
			var b bytes.Buffer
			if _, err := rewriteJSONLines(&b, bytes.NewReader(data), paths); err != nil {
				return err
			}
			data = b.Bytes()
		}
		hdr := &tar.Header{Name: strings.TrimPrefix(f.Path, id+"/"), Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// ExtractClaudeSidecar unpacks a sidecar from WriteClaudeSidecarForCloud, or
// from AO Cloud, into dir. It refuses paths that leave dir, anything but
// regular files, more than maxBytes in all and more than MaxSidecarEntries
// files.
func ExtractClaudeSidecar(r io.Reader, dir string, maxBytes int64) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("nativetransfer: read sidecar: %w", err)
	}
	tr := tar.NewReader(zr)
	var total int64
	for entries := 0; ; entries++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("nativetransfer: read sidecar: %w", err)
		}
		if entries == MaxSidecarEntries {
			return fmt.Errorf("%w: sidecar over %d files", ErrTooLarge, MaxSidecarEntries)
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("nativetransfer: sidecar entry %q is not a regular file", hdr.Name)
		}
		if err := validRelPath(hdr.Name); err != nil {
			return err
		}
		total += hdr.Size
		if total > maxBytes {
			return fmt.Errorf("%w: sidecar over %d bytes", ErrTooLarge, maxBytes)
		}
		target := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, hdr.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// WriteClaudeArchiveFromCloud writes an archive ImportClaude places on this
// machine from a transcript an AO Cloud sandbox kept in projectDir. The file
// at transcriptPath must be named <session id>.jsonl; sidecar files unpacked
// into the <session id>/ folder next to it are included.
func WriteClaudeArchiveFromCloud(ctx context.Context, w io.Writer, transcriptPath, projectDir string) (Manifest, error) {
	id, err := canonicalUUID(strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl"))
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: formatVersion, Harness: HarnessClaudeCode, NativeID: id, SourceProjectDir: projectDir}
	if m.SourceCwd, m.CLIVersion, err = claudeTranscriptFacts(transcriptPath); err != nil {
		return Manifest{}, err
	}
	if m.SourceCwd == "" {
		return Manifest{}, errors.New("nativetransfer: transcript records no working directory")
	}
	if m.Files, err = listClaudeFiles(filepath.Dir(transcriptPath), id); err != nil {
		return Manifest{}, err
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, writeArchive(ctx, w, m, filepath.Dir(transcriptPath))
}

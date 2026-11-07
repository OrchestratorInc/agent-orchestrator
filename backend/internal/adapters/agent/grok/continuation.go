package grok

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var importSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NativeSessionConfigDir resolves the provider state root used by a launch.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	lookup := func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	if root := strings.TrimSpace(lookup("GROK_HOME")); root != "" {
		return root, nil
	}
	home := lookup("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".grok"), nil
}

// LocateTranscript finds a confined native update log for the conversation.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if !importSessionID.MatchString(ref.NativeSessionID) {
		return "", false, nil
	}
	r, err := os.OpenRoot(ref.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = r.Close() }()
	var path string
	err = fs.WalkDir(r.FS(), "sessions", func(name string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			// As in discovery, an unreadable sibling is ineligible; errors
			// on the sessions root still prevent an authoritative lookup.
			if name != "sessions" && errors.Is(walkErr, fs.ErrPermission) {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if d.Name() == "updates.jsonl" && filepath.Base(filepath.Dir(name)) == ref.NativeSessionID && d.Type().IsRegular() {
			path = filepath.Join(ref.ConfigDir, name)
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return path, path != "", err
}

// ProbeNativeSession reports local backing state without modifying it.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	_, ok, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// Grok percent-encodes every non-alphanumeric byte of getcwd. A staging
// directory is renamed as a unit, so a failed copy never becomes resumable.
func grokImportBucket(cwd string) string {
	return strings.ReplaceAll(url.QueryEscape(cwd), "+", "%20")
}

// RelocateImportedSession prepares a private copy for the first native resume.
func (*Plugin) RelocateImportedSession(ctx context.Context, cfg ports.ImportedSessionRelocation) error {
	if !importSessionID.MatchString(cfg.NativeSessionID) || !filepath.IsAbs(cfg.LaunchCWD) {
		return errors.New("invalid grok import identity or launch directory")
	}
	cwd, err := filepath.EvalSymlinks(cfg.LaunchCWD)
	if err != nil {
		return err
	}
	bucket := grokImportBucket(cwd)
	if len(bucket) > 255 {
		return errors.New("grok import workspace path exceeds supported bucket length")
	}
	r, err := os.OpenRoot(cfg.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	src, err := filepath.Rel(cfg.ConfigDir, filepath.Dir(cfg.TranscriptPath))
	if err != nil || !filepath.IsLocal(src) || filepath.Base(src) != cfg.NativeSessionID {
		return errors.New("grok transcript outside provider home")
	}
	parent := filepath.Join("sessions", bucket)
	dest := filepath.Join(parent, cfg.NativeSessionID)
	if _, err := r.Stat(dest); err == nil {
		return nil // it may already contain AO's subsequent turns
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := r.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	stage := filepath.Join(parent, ".ao-import-"+hex.EncodeToString(nonce[:]))
	if err := r.Mkdir(stage, 0o700); err != nil {
		return err
	}
	defer func() { _ = r.RemoveAll(stage) }()
	for _, name := range []string{"summary.json", "updates.jsonl", "chat_history.jsonl", "system_prompt.txt", "prompt_context.json", "tool_definitions.json", "plan.json", "signals.json", "rewind_points.jsonl"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		in, err := r.Open(filepath.Join(src, name))
		if errors.Is(err, os.ErrNotExist) && name != "summary.json" && name != "updates.jsonl" && name != "chat_history.jsonl" {
			continue
		}
		if err != nil {
			return err
		}
		info, err := in.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<30 {
			_ = in.Close()
			return errors.New("grok source exceeds copy budget or is not a regular file")
		}
		out, err := r.OpenFile(filepath.Join(stage, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = in.Close()
			return err
		}
		if name == "summary.json" {
			var summary map[string]json.RawMessage
			err = json.NewDecoder(io.LimitReader(in, 4<<20)).Decode(&summary)
			if err == nil {
				// Only cwd changes; every other info key is kept as Grok wrote it.
				var info map[string]json.RawMessage
				err = json.Unmarshal(summary["info"], &info)
				var id string
				if err == nil {
					err = json.Unmarshal(info["id"], &id)
				}
				if err == nil && id != cfg.NativeSessionID {
					err = errors.New("grok summary identity mismatch")
				}
				if err == nil {
					info["cwd"], err = json.Marshal(cwd)
				}
				if err == nil {
					summary["info"], err = json.Marshal(info)
				}
				if err == nil {
					err = json.NewEncoder(out).Encode(summary)
				}
			}
		} else {
			_, err = io.Copy(out, io.LimitReader(in, info.Size()))
		}
		_ = in.Close()
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := copyGrokImportCheckpoints(ctx, r, src, stage); err != nil {
		return err
	}
	return r.Rename(stage, dest)
}

// Checkpoints may hold the compacted context required by the next native load.
func copyGrokImportCheckpoints(ctx context.Context, root *os.Root, src, stage string) error {
	base := filepath.Join(src, "compaction_checkpoints")
	return fs.WalkDir(root.FS(), base, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(walkErr, os.ErrNotExist) && path == base {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || !filepath.IsLocal(rel) {
			return errors.New("invalid grok checkpoint path")
		}
		dest := filepath.Join(stage, rel)
		if entry.IsDir() {
			return root.MkdirAll(dest, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<30 {
			return errors.New("invalid grok checkpoint file")
		}
		in, err := root.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := root.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, info.Size()))
		return errors.Join(copyErr, out.Close())
	})
}

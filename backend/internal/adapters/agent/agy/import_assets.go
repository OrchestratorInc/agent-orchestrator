package agy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const maxImportAssetBytes = 64 << 20
const maxImportAssetEntries = 4096

type importAsset struct {
	dir  bool
	data []byte
}

// Open one directory component at a time. The opened root must still be the
// directory inspected by Lstat; even links to another part of the profile are
// rejected, and subsequent reads are confined to the selected subtree.
func openImportAssetDir(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("agy: conversation asset directory is not a plain directory")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, errors.New("agy: conversation asset directory changed while opening")
	}
	return root, nil
}

// Agy 1.5.13 owns conversations/<id>.meta and brain/<id> alongside its DB;
// brain contains artifacts and .system_generated conversation logs. Nothing
// else in the provider profile is part of this snapshot. Missing optional
// assets stay missing, including an absent brain parent. Links inside brain/<id>
// are skipped, never followed. Native reopens check confinement/types without
// applying the initial snapshot's byte/count cap.
func readImportAssets(ctx context.Context, provider *os.Root, mode, id string, contents bool) (map[string]importAsset, error) {
	assets := make(map[string]importAsset)
	storage, err := openImportAssetDir(provider, mode)
	if err != nil {
		return nil, err
	}
	defer func() { _ = storage.Close() }()
	conversations, err := openImportAssetDir(storage, "conversations")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conversations.Close() }()
	var total int64
	readFile := func(root *os.Root, name, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("agy: conversation asset must be a regular file, not a link or special file")
		}
		asset := importAsset{}
		if contents {
			if info.Size() > maxImportAssetBytes-total {
				return errors.New("agy: conversation assets exceed snapshot budget")
			}
			f, err := root.Open(name)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			opened, err := f.Stat()
			if err != nil || !os.SameFile(info, opened) {
				return errors.New("agy: conversation asset changed while opening")
			}
			asset.data, err = io.ReadAll(io.LimitReader(f, maxImportAssetBytes-total+1))
			if err != nil {
				return err
			}
			total += int64(len(asset.data))
			if total > maxImportAssetBytes {
				return errors.New("agy: conversation assets exceed snapshot budget")
			}
		}
		assets[key] = asset
		return nil
	}
	meta := id + ".meta"
	if err := readFile(conversations, meta, filepath.Join("conversations", meta)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	brainParent, err := openImportAssetDir(storage, "brain")
	if errors.Is(err, os.ErrNotExist) {
		return assets, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = brainParent.Close() }()
	brain, err := openImportAssetDir(brainParent, id)
	if errors.Is(err, os.ErrNotExist) {
		return assets, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = brain.Close() }()
	err = fs.WalkDir(brain.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if (contents && len(assets) >= maxImportAssetEntries) || strings.Count(path, "/") > 32 {
			return errors.New("agy: conversation assets exceed snapshot traversal budget")
		}
		key := filepath.Join("brain", id, filepath.FromSlash(path))
		if entry.IsDir() {
			assets[key] = importAsset{dir: true}
			return nil
		}
		// Agy's scratch space can hold a venv or node_modules full of links.
		// They are never followed or copied, so they neither block the
		// snapshot nor a reopen; the conversation does not depend on them.
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		return readFile(brain, path, key)
	})
	return assets, err
}

func equalImportAssets(a, b map[string]importAsset) bool {
	return maps.EqualFunc(a, b, func(a, b importAsset) bool { return a.dir == b.dir && bytes.Equal(a.data, b.data) })
}

func writeImportAssets(stage, mode string, assets map[string]importAsset) error {
	for rel, asset := range assets {
		path := filepath.Join(stage, mode, rel)
		if asset.dir {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, asset.data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func validatePreparedImportAssets(ctx context.Context, ref ports.NativeSessionRef, proof agyImportProof) error {
	_, rel, err := preparedImportPath(ref.ConfigDir, ref.TranscriptPath)
	if err != nil {
		return err
	}
	mode := strings.Split(filepath.ToSlash(rel), "/")[0]
	root, err := os.OpenRoot(ref.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	assets, err := readImportAssets(ctx, root, mode, ref.NativeSessionID, false)
	if err != nil {
		return err
	}
	_, meta := assets[filepath.Join("conversations", ref.NativeSessionID+".meta")]
	_, brain := assets[filepath.Join("brain", ref.NativeSessionID)]
	if (proof.Meta && !meta) || (proof.Brain && !brain) {
		return errors.New("agy: prepared conversation is missing recorded native assets")
	}
	sourceMode := filepath.Base(filepath.Dir(filepath.Dir(proof.SourcePath)))
	for rel, asset := range assets {
		if asset.dir {
			continue
		}
		if source, err := os.Stat(filepath.Join(proof.SourceRoot, sourceMode, rel)); err == nil {
			if target, err := root.Stat(filepath.Join(mode, rel)); err == nil && os.SameFile(source, target) {
				return errors.New("agy: prepared assets must not share source inodes")
			}
		}
	}
	return nil
}

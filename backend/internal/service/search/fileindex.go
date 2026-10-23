package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2/mapping"
)

// Workspace is what a session's file search covers: the worktree root and
// every tracked or untracked, non-ignored path in it.
type Workspace struct {
	Root  string
	Paths []string // slash-separated, relative to Root
}

const (
	chunkLines    = 40
	chunkOverlap  = 10
	batchDocs     = 250
	maxIndexBytes = 512 * 1024 // larger files are found by name only
	maxIndexFiles = 200_000
	// saveEveryDocs bounds how much an interrupted first build repeats.
	saveEveryDocs = 2000
	// maxAvgLineBytes skips minified and generated one-line blobs.
	maxAvgLineBytes = 300
	internalBuilt   = "ao.built"
)

type chunkDoc struct {
	Content   string `json:"content"`
	StartLine int    `json:"start_line"`
}

// fileEntry is what the index knows about one path: enough to tell whether
// it changed and which chunk documents it owns.
type fileEntry struct {
	Size   int64 `json:"s"`
	ModNs  int64 `json:"m"`
	Chunks []int `json:"c,omitempty"` // 1-based start lines of the path's chunk docs
}

// fileIndex is one session's chunked content index.
type fileIndex struct {
	disk *diskIndex

	// reconcileMu serializes writers; searches never take it.
	reconcileMu sync.Mutex

	mu            sync.RWMutex
	manifest      map[string]fileEntry
	paths         []string
	root          string // worktree root from the last reconcile
	built         bool
	reconciling   bool
	lastReconcile time.Time
	users         int // guarded by fileIndexes.mu
}

func openFileIndex(dir string, m *mapping.IndexMappingImpl) (*fileIndex, error) {
	disk, err := openDiskIndex(dir, m, false)
	if err != nil {
		return nil, err
	}
	fi := &fileIndex{disk: disk, manifest: make(map[string]fileEntry)}
	if err := disk.getJSON(internalManifest, &fi.manifest); err != nil {
		fi.manifest = make(map[string]fileEntry)
	}
	if raw, err := disk.bleve.GetInternal([]byte(internalBuilt)); err == nil && string(raw) == "1" {
		fi.built = true
	}
	fi.paths = slices.Sorted(maps.Keys(fi.manifest))
	return fi, nil
}

// splitChunks cuts text into overlapping 40-line windows.
func splitChunks(text string) []chunkDoc {
	lines := strings.Split(text, "\n")
	var out []chunkDoc
	for start := 0; start < len(lines); start += chunkLines - chunkOverlap {
		end := min(start+chunkLines, len(lines))
		out = append(out, chunkDoc{Content: strings.Join(lines[start:end], "\n"), StartLine: start + 1})
		if end == len(lines) {
			break
		}
	}
	return out
}

// indexableText rejects binaries (a NUL in the first 8 KB) and minified or
// generated blobs (very long average lines).
func indexableText(data []byte) bool {
	head := data[:min(len(data), 8192)]
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	lines := bytes.Count(data, []byte{'\n'}) + 1
	return len(data)/lines <= maxAvgLineBytes
}

// safeJoin resolves a workspace-relative path, refusing anything that could
// leave the root.
func safeJoin(root, rel string) (string, bool) {
	local := filepath.FromSlash(rel)
	if !filepath.IsLocal(local) {
		return "", false
	}
	return filepath.Join(root, local), true
}

// reconcile brings the index in line with ws: unchanged files (same size and
// modification time) are skipped, changed files are re-chunked, removed files
// lose their chunks. A cancelled reconcile keeps the batches it committed;
// the next one picks up from there.
func (fi *fileIndex) reconcile(ctx context.Context, ws Workspace, now func() time.Time) error {
	if !fi.reconcileMu.TryLock() {
		return nil // another reconcile is already doing this work
	}
	defer fi.reconcileMu.Unlock()
	fi.mu.Lock()
	fi.reconciling = true
	manifest := maps.Clone(fi.manifest)
	fi.mu.Unlock()
	defer func() {
		fi.mu.Lock()
		fi.reconciling = false
		fi.mu.Unlock()
	}()

	paths := ws.Paths
	if len(paths) > maxIndexFiles {
		paths = paths[:maxIndexFiles]
	}
	wanted := make(map[string]struct{}, len(paths))
	batch := fi.disk.bleve.NewBatch()
	pending, sinceSave := 0, 0
	flush := func(force bool) error {
		if pending > 0 && (force || batch.Size() >= batchDocs) {
			if err := fi.disk.bleve.Batch(batch); err != nil {
				return err
			}
			batch = fi.disk.bleve.NewBatch()
			sinceSave += pending
			pending = 0
		}
		if force || sinceSave >= saveEveryDocs {
			if err := fi.publish(manifest, false); err != nil {
				return err
			}
			sinceSave = 0
		}
		return nil
	}

	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			_ = flush(true)
			return err
		}
		full, ok := safeJoin(ws.Root, rel)
		if !ok {
			continue
		}
		wanted[rel] = struct{}{}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			// Gone, a directory or a symlink: listed by name, never read.
			if old, had := manifest[rel]; had {
				for _, start := range old.Chunks {
					batch.Delete(docID(rel, start))
					pending++
				}
			}
			manifest[rel] = fileEntry{}
			continue
		}
		old, had := manifest[rel]
		if had && old.Size == info.Size() && old.ModNs == info.ModTime().UnixNano() {
			continue
		}
		entry := fileEntry{Size: info.Size(), ModNs: info.ModTime().UnixNano()}
		keep := map[int]bool{}
		if info.Size() <= maxIndexBytes {
			if data, err := os.ReadFile(full); err == nil && indexableText(data) {
				for _, c := range splitChunks(string(data)) {
					if err := batch.Index(docID(rel, c.StartLine), c); err != nil {
						return err
					}
					fi.disk.vocab.addText(c.Content)
					entry.Chunks = append(entry.Chunks, c.StartLine)
					keep[c.StartLine] = true
					pending++
				}
			}
		}
		for _, start := range old.Chunks {
			if !keep[start] {
				batch.Delete(docID(rel, start))
				pending++
			}
		}
		manifest[rel] = entry
		if err := flush(false); err != nil {
			return err
		}
	}
	for rel, old := range manifest {
		if _, ok := wanted[rel]; ok {
			continue
		}
		for _, start := range old.Chunks {
			batch.Delete(docID(rel, start))
			pending++
		}
		delete(manifest, rel)
	}
	if pending > 0 {
		if err := fi.disk.bleve.Batch(batch); err != nil {
			return err
		}
	}
	if err := fi.publish(manifest, true); err != nil {
		return err
	}
	fi.mu.Lock()
	fi.lastReconcile = now()
	fi.root = ws.Root
	fi.mu.Unlock()
	return nil
}

// snapshot is the workspace as of the last reconcile: enough to search
// without asking the session for its file list again.
func (fi *fileIndex) snapshot() Workspace {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	return Workspace{Root: fi.root, Paths: fi.paths}
}

// publish persists the manifest and vocabulary and makes the new path list
// visible to searches.
func (fi *fileIndex) publish(manifest map[string]fileEntry, complete bool) error {
	if err := fi.disk.putJSON(internalManifest, manifest); err != nil {
		return err
	}
	if err := fi.disk.saveVocab(); err != nil {
		return err
	}
	if complete {
		if err := fi.disk.bleve.SetInternal([]byte(internalBuilt), []byte("1")); err != nil {
			return err
		}
	}
	snapshot := maps.Clone(manifest)
	fi.mu.Lock()
	fi.manifest = snapshot
	fi.paths = slices.Sorted(maps.Keys(snapshot))
	if complete {
		fi.built = true
	}
	fi.mu.Unlock()
	return nil
}

type fileIndexState struct {
	built, reconciling bool
	lastReconcile      time.Time
}

func (fi *fileIndex) state() fileIndexState {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	return fileIndexState{built: fi.built, reconciling: fi.reconciling, lastReconcile: fi.lastReconcile}
}

// readChunk returns the current text of the chunk starting at line start.
func readChunk(root, rel string, start int, cache map[string][]string) (string, bool) {
	lines, ok := cache[rel]
	if !ok {
		full, safe := safeJoin(root, rel)
		if !safe {
			return "", false
		}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxIndexBytes {
			cache[rel] = nil
			return "", false
		}
		data, err := os.ReadFile(full)
		if err != nil {
			cache[rel] = nil
			return "", false
		}
		lines = strings.Split(string(data), "\n")
		cache[rel] = lines
	}
	if lines == nil || start < 1 || start > len(lines) {
		return "", false
	}
	end := min(start-1+chunkLines, len(lines))
	return strings.Join(lines[start-1:end], "\n"), true
}

// fileIndexes owns the per-session indexes under dir: a bounded set open at
// once, a bounded set kept on disk.
type fileIndexes struct {
	dir       string
	mapping   *mapping.IndexMappingImpl
	maxOpen   int
	maxOnDisk int

	mu    sync.Mutex
	open  map[string]*fileIndex
	order []string // least recently used first
}

func newFileIndexes(dir string, m *mapping.IndexMappingImpl) *fileIndexes {
	return &fileIndexes{dir: dir, mapping: m, maxOpen: 8, maxOnDisk: 24, open: make(map[string]*fileIndex)}
}

var safeDirName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

func indexDirName(session string) string {
	if safeDirName.MatchString(session) {
		return session
	}
	sum := sha256.Sum256([]byte(session))
	return "s-" + hex.EncodeToString(sum[:16])
}

// acquire opens (or creates) a session's index and pins it open until
// release is called.
func (m *fileIndexes) acquire(session string) (*fileIndex, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fi, ok := m.open[session]
	if !ok {
		if err := os.MkdirAll(m.dir, 0o700); err != nil {
			return nil, nil, err
		}
		dir := filepath.Join(m.dir, indexDirName(session))
		var err error
		fi, err = openFileIndex(dir, m.mapping)
		if err != nil {
			return nil, nil, err
		}
		m.open[session] = fi
		m.evictLocked(session)
	}
	m.touchLocked(session)
	fi.users++
	var once sync.Once
	return fi, func() {
		once.Do(func() {
			m.mu.Lock()
			fi.users--
			m.mu.Unlock()
		})
	}, nil
}

func (m *fileIndexes) touchLocked(session string) {
	for i, s := range m.order {
		if s == session {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.order = append(m.order, session)
	now := time.Now()
	_ = os.Chtimes(filepath.Join(m.dir, indexDirName(session)), now, now)
}

// evictLocked closes idle indexes beyond maxOpen and deletes the least
// recently used index directories beyond maxOnDisk.
func (m *fileIndexes) evictLocked(keep string) {
	for len(m.open) > m.maxOpen {
		victim := ""
		for _, s := range m.order {
			if fi := m.open[s]; s != keep && fi != nil && fi.users == 0 {
				victim = s
				break
			}
		}
		if victim == "" {
			break
		}
		_ = m.open[victim].disk.close()
		delete(m.open, victim)
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil || len(entries) <= m.maxOnDisk {
		return
	}
	type aged struct {
		name string
		mod  time.Time
	}
	var dirs []aged
	openDirs := map[string]bool{}
	for s := range m.open {
		openDirs[indexDirName(s)] = true
	}
	for _, e := range entries {
		if !e.IsDir() || openDirs[e.Name()] {
			continue
		}
		if info, err := e.Info(); err == nil {
			dirs = append(dirs, aged{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.Before(dirs[j].mod) })
	excess := len(entries) - m.maxOnDisk
	for i := 0; i < excess && i < len(dirs); i++ {
		_ = os.RemoveAll(filepath.Join(m.dir, dirs[i].name))
	}
}

func (m *fileIndexes) closeAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	for s, fi := range m.open {
		if err := fi.disk.close(); err != nil && first == nil {
			first = fmt.Errorf("close search index %s: %w", s, err)
		}
		delete(m.open, s)
	}
	return first
}

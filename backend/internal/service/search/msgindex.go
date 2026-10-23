package search

import (
	"context"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// MessageStore is the chat side of search: catch-up reads for the indexer
// and query-time re-reads, so a hit always shows the store's current text.
// The SQLite store satisfies it.
type MessageStore interface {
	SearchMessagesAfter(ctx context.Context, updatedAt time.Time, id string, limit int) ([]domain.SearchMessageChange, error)
	SearchMessageIDsAfter(ctx context.Context, id string, limit int) ([]string, error)
	SearchMessagesByID(ctx context.Context, ids []string) ([]domain.SearchMessage, error)
	SearchVisibleMessageIDs(ctx context.Context, conversationID string, ids []string) (map[string]bool, error)
}

const (
	messageChunkWords   = 120
	messageChunkOverlap = 30
	messagePageSize     = 500
)

type messageDoc struct {
	Content string `json:"content"`
}

type messageCursor struct {
	UpdatedAt time.Time `json:"u"`
	ID        string    `json:"i"`
}

// messageIndex is the daemon-wide chat index. Each finished message is one
// document, or several overlapping 120-word chunks when long; a message
// still streaming is not searchable until it settles.
type messageIndex struct {
	disk  *diskIndex
	store MessageStore

	syncMu sync.Mutex

	mu       sync.RWMutex
	chunks   map[string]int // message id -> chunk docs it owns
	cursor   messageCursor
	caughtUp bool
}

func openMessageIndex(dir string, m *mapping.IndexMappingImpl, store MessageStore) (*messageIndex, error) {
	disk, err := openDiskIndex(dir, m, true)
	if err != nil {
		return nil, err
	}
	mi := &messageIndex{disk: disk, store: store, chunks: make(map[string]int)}
	if err := disk.getJSON(internalManifest, &mi.chunks); err != nil {
		mi.chunks = make(map[string]int)
	}
	_ = disk.getJSON(internalCursor, &mi.cursor)
	return mi, nil
}

// splitMessageChunks keeps short messages whole and cuts long ones into
// overlapping word windows, so proximity is judged within a passage.
func splitMessageChunks(text string) []string {
	words := strings.Fields(text)
	if len(words) <= messageChunkWords+messageChunkOverlap {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{text}
	}
	var out []string
	for start := 0; start < len(words); start += messageChunkWords - messageChunkOverlap {
		end := min(start+messageChunkWords, len(words))
		out = append(out, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
	}
	return out
}

// syncOnce indexes every message changed since the cursor. Safe to call
// concurrently; overlapping calls return immediately.
func (mi *messageIndex) syncOnce(ctx context.Context) error {
	if !mi.syncMu.TryLock() {
		return nil
	}
	defer mi.syncMu.Unlock()
	mi.mu.RLock()
	cursor, chunks := mi.cursor, maps.Clone(mi.chunks)
	mi.mu.RUnlock()
	// The cursor and chunk counts are saved together every few pages and at
	// the end; a crash in between only re-reads those pages (idempotent).
	dirty := false
	for pages := 1; ; pages++ {
		if err := ctx.Err(); err != nil {
			if dirty {
				_ = mi.persist(chunks, cursor)
			}
			return err
		}
		rows, err := mi.store.SearchMessagesAfter(ctx, cursor.UpdatedAt, cursor.ID, messagePageSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		batch := mi.disk.bleve.NewBatch()
		for _, row := range rows {
			var parts []string
			if !row.Streaming {
				parts = splitMessageChunks(row.Text)
			}
			for k, part := range parts {
				if err := batch.Index(docID(row.ID, k), messageDoc{Content: part}); err != nil {
					return err
				}
				mi.disk.vocab.addText(part)
			}
			for k := len(parts); k < chunks[row.ID]; k++ {
				batch.Delete(docID(row.ID, k))
			}
			if len(parts) == 0 {
				delete(chunks, row.ID)
			} else {
				chunks[row.ID] = len(parts)
			}
		}
		if err := mi.disk.bleve.Batch(batch); err != nil {
			return err
		}
		last := rows[len(rows)-1]
		cursor = messageCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
		dirty = true
		if pages%10 == 0 {
			if err := mi.persist(chunks, cursor); err != nil {
				return err
			}
			dirty = false
		}
		if len(rows) < messagePageSize {
			break
		}
	}
	if dirty {
		if err := mi.persist(chunks, cursor); err != nil {
			return err
		}
	}
	mi.mu.Lock()
	mi.caughtUp = true
	mi.mu.Unlock()
	return nil
}

func (mi *messageIndex) persist(chunks map[string]int, cursor messageCursor) error {
	if err := mi.disk.putJSON(internalManifest, chunks); err != nil {
		return err
	}
	if err := mi.disk.putJSON(internalCursor, cursor); err != nil {
		return err
	}
	if err := mi.disk.saveVocab(); err != nil {
		return err
	}
	mi.mu.Lock()
	mi.chunks, mi.cursor = maps.Clone(chunks), cursor
	mi.mu.Unlock()
	return nil
}

// prune drops messages the store no longer holds (deleted sessions cascade
// their conversations away without bumping any cursor).
func (mi *messageIndex) prune(ctx context.Context) error {
	present := make(map[string]bool)
	after := ""
	for {
		ids, err := mi.store.SearchMessageIDsAfter(ctx, after, 5000)
		if err != nil {
			return err
		}
		for _, id := range ids {
			present[id] = true
		}
		if len(ids) < 5000 {
			break
		}
		after = ids[len(ids)-1]
	}
	mi.mu.RLock()
	var gone []string
	for id := range mi.chunks {
		if !present[id] {
			gone = append(gone, id)
		}
	}
	mi.mu.RUnlock()
	return mi.remove(gone)
}

// remove deletes messages from the index, e.g. hits the store no longer has.
func (mi *messageIndex) remove(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	mi.syncMu.Lock()
	defer mi.syncMu.Unlock()
	mi.mu.RLock()
	cursor, chunks := mi.cursor, maps.Clone(mi.chunks)
	mi.mu.RUnlock()
	batch := mi.disk.bleve.NewBatch()
	for _, id := range ids {
		n := max(chunks[id], 1)
		for k := 0; k < n; k++ {
			batch.Delete(docID(id, k))
		}
		delete(chunks, id)
	}
	if err := mi.disk.bleve.Batch(batch); err != nil {
		return err
	}
	return mi.persist(chunks, cursor)
}

func (mi *messageIndex) ready() bool {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	return mi.caughtUp
}

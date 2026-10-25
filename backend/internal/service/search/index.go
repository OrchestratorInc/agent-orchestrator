package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
)

// schemaVersion is stored inside every index. Bump it whenever the mapping,
// analyzers or document layout change: an index with another version is
// deleted and rebuilt from source, never migrated.
const schemaVersion = "3"

const (
	internalSchema   = "ao.schema"
	internalManifest = "ao.manifest"
	internalVocab    = "ao.vocab"
	internalCursor   = "ao.cursor"
)

// diskIndex is one Bleve index on disk plus its vocabulary. It is derived
// data: deleting the directory is always safe.
type diskIndex struct {
	dir   string
	bleve bleve.Index
	vocab *vocabulary
	// phrases reports whether the index keeps word positions, so phrase
	// queries can match at all.
	phrases bool
}

// openDiskIndex opens dir, or creates it when missing, unreadable or built
// with another schema version. phrases says whether m keeps word positions.
func openDiskIndex(dir string, m *mapping.IndexMappingImpl, phrases bool) (*diskIndex, error) {
	d := &diskIndex{dir: dir, vocab: newVocabulary(), phrases: phrases}
	if idx, err := bleve.Open(dir); err == nil {
		if version, verr := idx.GetInternal([]byte(internalSchema)); verr == nil && string(version) == schemaVersion {
			d.bleve = idx
			_ = d.getJSON(internalVocab, d.vocab)
			return d, nil
		}
		_ = idx.Close()
	}
	// Missing, corrupt, half-written or another schema: rebuild from source.
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("reset search index: %w", err)
	}
	idx, err := bleve.NewUsing(dir, m, bleve.Config.DefaultIndexType, bleve.Config.DefaultKVStore, nil)
	if err != nil {
		return nil, fmt.Errorf("create search index: %w", err)
	}
	if err := idx.SetInternal([]byte(internalSchema), []byte(schemaVersion)); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("stamp search index: %w", err)
	}
	d.bleve = idx
	return d, nil
}

// docID names a document: a path or message id plus its chunk's start line
// or ordinal.
func docID(key string, n int) string { return key + "#" + strconv.Itoa(n) }

// splitDocID reverses docID.
func splitDocID(id string) (string, int) {
	i := strings.LastIndex(id, "#")
	if i < 0 {
		return id, 0
	}
	n, _ := strconv.Atoi(id[i+1:])
	return id[:i], n
}

func (d *diskIndex) close() error { return d.bleve.Close() }

// getJSON decodes a stored value into v, leaving v untouched when the key
// was never written.
func (d *diskIndex) getJSON(key string, v any) error {
	raw, err := d.bleve.GetInternal([]byte(key))
	if err != nil || len(raw) == 0 {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (d *diskIndex) putJSON(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return d.bleve.SetInternal([]byte(key), raw)
}

func (d *diskIndex) saveVocab() error { return d.putJSON(internalVocab, d.vocab) }

// docFreq returns how many documents contain the indexed term, and the
// total document count.
func (d *diskIndex) docFreq(ctx context.Context, term string) (df, docs uint64) {
	n, err := d.bleve.DocCount()
	if err != nil {
		return 0, 0
	}
	adv, err := d.bleve.Advanced()
	if err != nil {
		return 0, n
	}
	r, err := adv.Reader()
	if err != nil {
		return 0, n
	}
	defer func() { _ = r.Close() }()
	tfr, err := r.TermFieldReader(ctx, []byte(term), fieldContent, false, false, false)
	if err != nil {
		return 0, n
	}
	df = tfr.Count()
	_ = tfr.Close()
	return df, n
}

type rawHit struct {
	id    string
	score float64
	line  int
}

// searchHits runs one retrieval pass and normalizes scores by the pass's top
// hit, so BM25 only ever acts as a tie-break between passes.
func (d *diskIndex) searchHits(ctx context.Context, q query.Query, size int) ([]rawHit, error) {
	req := bleve.NewSearchRequestOptions(q, size, 0, false)
	req.Fields = []string{fieldStartLine}
	res, err := d.bleve.SearchInContext(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]rawHit, 0, len(res.Hits))
	top := 1.0
	if len(res.Hits) > 0 && res.Hits[0].Score > 0 {
		top = res.Hits[0].Score
	}
	for _, h := range res.Hits {
		hit := rawHit{id: h.ID, score: h.Score / top}
		if v, ok := h.Fields[fieldStartLine].(float64); ok {
			hit.line = int(v)
		}
		out = append(out, hit)
	}
	return out, nil
}

// combinedIDF is the IDF of a term over several indexes taken as one corpus,
// so file chunks and chat messages are weighted on the same scale.
func combinedIDF(ctx context.Context, indexes ...*diskIndex) idfFunc {
	return func(term string) (float64, bool) {
		var df, docs uint64
		for _, d := range indexes {
			if d == nil {
				continue
			}
			f, n := d.docFreq(ctx, term)
			df += f
			docs += n
		}
		if df == 0 {
			// A word no document has is almost certainly a typo of a word
			// the user cares about: weigh it as a rare word.
			return bm25IDF(float64(docs)+1, 1), false
		}
		return bm25IDF(float64(docs), float64(df)), true
	}
}

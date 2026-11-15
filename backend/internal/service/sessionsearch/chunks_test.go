package sessionsearch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextChunksKeepTheirBreaksAndCarryTheOverlap(t *testing.T) {
	const limit, overlap = 1024, 96
	value := strings.Repeat("alpha beta gamma delta\n", 400)
	plain := textChunks(value, limit, 0, 8)
	if got := strings.Join(plain, ""); got != value[:len(got)] || len(plain) != 8 {
		t.Fatalf("without an overlap the %d chunks must be the text in order", len(plain))
	}
	chunks := textChunks(value, limit, overlap, 8)
	if len(chunks) != len(plain) || chunks[0] != plain[0] {
		t.Fatalf("chunks = %d (first equal: %v), want the same %d breaks", len(chunks), chunks[0] == plain[0], len(plain))
	}
	end := len(plain[0])
	for index := 1; index < len(chunks); index++ {
		carried, ok := strings.CutSuffix(chunks[index], plain[index])
		if !ok {
			t.Fatalf("chunk %d does not end with its own text", index)
		}
		if len(carried) < overlap || len(carried) > overlap+chunkOverlapWordBytes {
			t.Fatalf("chunk %d carries %d bytes, want %d to %d", index, len(carried), overlap, overlap+chunkOverlapWordBytes)
		}
		if carried != value[end-len(carried):end] {
			t.Fatalf("chunk %d carries %q, not the text before it", index, carried)
		}
		if before := value[end-len(carried)-1]; before != ' ' && before != '\n' {
			t.Fatalf("chunk %d's overlap starts inside a word (after %q)", index, before)
		}
		end += len(plain[index])
	}
}

func TestTextChunksHoldAPhraseAcrossABreak(t *testing.T) {
	value := strings.Repeat("x ", 4092) + "alpha beta"
	chunks := textChunks(value, maxDocBodyBytes, chunkOverlapBytes, maxMessageChunks)
	if len(chunks) != 2 || !strings.HasSuffix(chunks[0], "alpha ") {
		t.Fatalf("chunks = %d, first ends %q; the fixture must break between the words", len(chunks), chunks[0][len(chunks[0])-8:])
	}
	if !strings.Contains(chunks[1], "alpha beta") {
		t.Fatalf("second chunk %q does not hold the phrase", chunks[1])
	}
}

func TestTextChunksOfUnbrokenTextStayValidUTF8(t *testing.T) {
	value := strings.Repeat("é", 5000) + strings.Repeat("日本語", 3000)
	chunks := textChunks(value, 1000, 100, 64)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want several", len(chunks))
	}
	for index, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d is not valid UTF-8", index)
		}
	}
	plain := textChunks(value, 1000, 0, 64)
	for index := range chunks {
		if !strings.HasSuffix(chunks[index], plain[index]) {
			t.Fatalf("chunk %d does not end with its own text", index)
		}
	}
}

func TestTextChunksBounds(t *testing.T) {
	if got := textChunks("short", 1024, 96, 8); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short text = %q, want itself", got)
	}
	value := strings.Repeat("word ", 2000)
	if got := textChunks(value, 64, 64, 1000); len(got) == 0 || len(got) > 1000 {
		t.Fatalf("chunks = %d with overlap == limit", len(got))
	}
	if got := textChunks(value, 64, 16, 3); len(got) != 3 {
		t.Fatalf("chunks = %d, want the bound of 3", len(got))
	}
}

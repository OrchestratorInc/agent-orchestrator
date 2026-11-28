package terminal

import (
	"bytes"
	"fmt"
	"testing"
)

// BenchmarkOutQueueDataFrame measures one terminal output frame on its way to a
// client: the producer's chunk, the queue, and the base64 encode in drain.
func BenchmarkOutQueueDataFrame(b *testing.B) {
	for _, size := range []int{4 << 10, 32 << 10, 256 << 10} {
		payload := bytes.Repeat([]byte("a"), size)
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			q := newOutQueue()
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Producers hand over a slice they never reuse, which is what
				// the attachment read loop does for every read.
				q.push(serverMsg{Ch: chTerminal, ID: "t1", Type: msgData, raw: append([]byte(nil), payload...)})
				if frames := q.drain(); len(frames) != 1 {
					b.Fatalf("drain returned %d frames, want 1", len(frames))
				}
			}
		})
	}
}

// BenchmarkOutQueueMergedBurst measures a burst that merges: 32 chunks queued
// behind one writer wake, the shape a flood of small PTY reads takes.
func BenchmarkOutQueueMergedBurst(b *testing.B) {
	const (
		chunks    = 32
		chunkSize = 1 << 10
	)
	payload := bytes.Repeat([]byte("a"), chunkSize)
	q := newOutQueue()
	b.SetBytes(chunks * chunkSize)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := 0; j < chunks; j++ {
			q.push(serverMsg{Ch: chTerminal, ID: "t1", Type: msgData, raw: append([]byte(nil), payload...)})
		}
		_ = q.drain()
	}
}

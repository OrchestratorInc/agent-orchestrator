package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const maxJournalBytes int64 = 256 << 20

var errJournalFull = errors.New("persistent provider journal is full")

// frameJournal is a bounded, host-owned WAL for provider notifications
// emitted during one active prompt. It is intentionally separate from AO's
// SQLite projection: the host appends provider bytes before delivery, while the
// daemon owns semantic normalization and transactional deduplication.
type frameJournal struct {
	path string
	file *os.File
	size int64
}

type journalSpan struct {
	offset int64
	length int64
}

func openFrameJournal(ctx context.Context, path string) (*frameJournal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open persistent provider journal: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure persistent provider journal: %w", err)
	}
	return &frameJournal{path: path, file: file}, nil
}

func (j *frameJournal) append(ctx context.Context, frame []byte) (journalSpan, error) {
	if err := ctx.Err(); err != nil {
		return journalSpan{}, err
	}
	if int64(len(frame)) > maxJournalBytes-j.size {
		return journalSpan{}, fmt.Errorf("%w: limit=%d", errJournalFull, maxJournalBytes)
	}
	span := journalSpan{offset: j.size, length: int64(len(frame))}
	written, err := j.file.Write(frame)
	j.size += int64(written)
	if err != nil {
		return journalSpan{}, fmt.Errorf("append persistent provider journal: %w", err)
	}
	if written != len(frame) {
		return journalSpan{}, io.ErrShortWrite
	}
	return span, nil
}

func (j *frameJournal) replayTo(ctx context.Context, dst io.Writer, span journalSpan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if span.length == 0 {
		return nil
	}
	if span.offset < 0 || span.length < 0 || span.offset+span.length > j.size {
		return errors.New("invalid persistent provider journal span")
	}
	_, err := io.Copy(dst, io.NewSectionReader(j.file, span.offset, span.length))
	return err
}

func (j *frameJournal) reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := j.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate persistent provider journal: %w", err)
	}
	if _, err := j.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind persistent provider journal: %w", err)
	}
	j.size = 0
	return nil
}

func (j *frameJournal) close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(j.file.Close(), os.Remove(j.path))
}

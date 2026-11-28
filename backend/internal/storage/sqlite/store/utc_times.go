package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// utcTimes is how every sqlc query in the store reaches the database. It puts
// each time bound to a statement in UTC.
//
// The driver stores a time.Time as the text of its String(). That text must be
// one the driver can read back, and it is compared as text in SQL, so every
// stored time has to be the UTC rendering (migration 0120 repaired an earlier
// local-zone leak). A time in any other zone breaks both:
//
//   - a zone whose name is a four-digit offset, whether a fixed zone parsed from
//     JSON ("2026-10-21T18:15:40+02:00" decoded in a daemon that is not at +02:00)
//     or a real one (Nepal, Iran, Afghanistan, Myanmar, Sri Lanka), is written as
//     "+0545 +0545" and cannot be scanned back into a time.Time, so listing the
//     table fails (#7026, #6890);
//   - a local wall clock sorts wrongly against UTC in a lexicographic comparison.
//
// Doing it here covers every writer, including ones added later, instead of
// relying on each call site to remember .UTC().
type utcTimes struct{ db gen.DBTX }

func (u utcTimes) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return u.db.ExecContext(ctx, query, utcArgs(args)...)
}

// PrepareContext is passed through: a statement prepared here takes its
// arguments later, where they cannot be converted. sqlc is configured without
// prepared queries, so nothing in the store prepares one.
func (u utcTimes) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return u.db.PrepareContext(ctx, query)
}

func (u utcTimes) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return u.db.QueryContext(ctx, query, utcArgs(args)...)
}

func (u utcTimes) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return u.db.QueryRowContext(ctx, query, utcArgs(args)...)
}

// utcArgs returns args with every time in UTC and without its monotonic
// reading. It copies on the first change, so the caller's slice is never
// modified and arguments with no time cost nothing.
func utcArgs(args []any) []any {
	var out []any
	set := func(i int, v any) {
		if out == nil {
			out = make([]any, len(args))
			copy(out, args)
		}
		out[i] = v
	}
	for i, arg := range args {
		switch v := arg.(type) {
		case time.Time:
			if v.Location() != time.UTC {
				set(i, v.UTC())
			}
		case *time.Time:
			if v != nil && v.Location() != time.UTC {
				u := v.UTC()
				set(i, &u)
			}
		case sql.NullTime:
			if v.Valid && v.Time.Location() != time.UTC {
				set(i, sql.NullTime{Time: v.Time.UTC(), Valid: true})
			}
		case sql.NamedArg:
			if t, ok := v.Value.(time.Time); ok && t.Location() != time.UTC {
				set(i, sql.NamedArg{Name: v.Name, Value: t.UTC()})
			}
		}
	}
	if out == nil {
		return args
	}
	return out
}

// txQueries is the sqlc query set for tx, with the same time handling as the
// pooled query sets. Use it instead of Queries.WithTx, which would bypass it.
func (s *Store) txQueries(tx *sql.Tx) *gen.Queries {
	return gen.New(utcTimes{tx})
}

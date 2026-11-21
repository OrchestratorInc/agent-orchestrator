package devin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestImportPreviewsPropagatesCancellationDuringFinalArchive(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "cancelled"}[cancelled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			queries := 0
			d := &previewErrorConnector{query: func() error {
				queries++
				if cancelled {
					cancel()
					return ctx.Err()
				}
				return errors.New("malformed native archive")
			}}
			db := sql.OpenDB(d)
			defer func() { _ = db.Close() }()
			previews, err := readImportPreviews(ctx, &importStore{db: db}, []ImportArchive{{ID: nativeImportTestID}})
			if queries != 1 {
				t.Fatalf("archive read did not execute: %d", queries)
			}
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("final archive cancellation was lost: %v", err)
			}
			if !cancelled && (err != nil || len(previews) != 0) {
				t.Fatalf("malformed archive was not skipped: %#v,%v", previews, err)
			}
		})
	}
}

// The connection fails during the archive query rather than before the loop.
// This makes the final-candidate cancellation boundary deterministic.
type previewErrorConnector struct{ query func() error }

func (d *previewErrorConnector) Connect(context.Context) (driver.Conn, error) {
	return &previewErrorConn{query: d.query}, nil
}
func (d *previewErrorConnector) Driver() driver.Driver { return d }
func (d *previewErrorConnector) Open(string) (driver.Conn, error) {
	return d.Connect(context.Background())
}

type previewErrorConn struct{ query func() error }

func (*previewErrorConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*previewErrorConn) Close() error                        { return nil }
func (*previewErrorConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *previewErrorConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, c.query()
}

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/timestamps"
)

// Read legacy times while the bounded repair catches up. Keep the driver's
// declared column metadata: timestamp-looking TEXT and BLOB values stay text.
func init() {
	db, err := sql.Open("sqlite", "")
	if err != nil {
		panic(err)
	}
	sql.Register("ao-sqlite", timestampDriver{db.Driver()})
	_ = db.Close()
}

type timestampDriver struct{ driver.Driver }

func (d timestampDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	if conn, ok := c.(sqliteConn); ok {
		return timestampConn{conn}, nil
	}
	_ = c.Close()
	return nil, fmt.Errorf("sqlite driver lacks required connection interfaces")
}

// Preserve modernc's context cancellation, transactions and pool validation.
type sqliteConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}
type timestampConn struct{ sqliteConn }

func (c timestampConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	r, err := c.sqliteConn.QueryContext(ctx, q, args)
	return timestampResult(r, err)
}
func (c timestampConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}
func (c timestampConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	s, err := c.sqliteConn.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	if stmt, ok := s.(sqliteStmt); ok {
		return timestampStmt{stmt}, nil
	}
	_ = s.Close()
	return nil, fmt.Errorf("sqlite driver lacks required statement interfaces")
}

type sqliteStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}
type timestampStmt struct{ sqliteStmt }

func (s timestampStmt) Query(args []driver.Value) (driver.Rows, error) {
	r, err := s.sqliteStmt.Query(args)
	return timestampResult(r, err)
}
func (s timestampStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	r, err := s.sqliteStmt.QueryContext(ctx, args)
	return timestampResult(r, err)
}

type sqliteRows interface {
	driver.Rows
	driver.RowsColumnTypeDatabaseTypeName
	driver.RowsColumnTypeLength
	driver.RowsColumnTypeNullable
	driver.RowsColumnTypePrecisionScale
	driver.RowsColumnTypeScanType
}
type timestampRows struct {
	sqliteRows
	times []int
}

func timestampResult(r driver.Rows, err error) (driver.Rows, error) {
	if err != nil {
		return nil, err
	}
	if rows, ok := r.(sqliteRows); ok {
		out := timestampRows{sqliteRows: rows}
		for i := range rows.Columns() {
			switch rows.ColumnTypeDatabaseTypeName(i) {
			case "TIMESTAMP", "DATETIME", "DATE":
				out.times = append(out.times, i)
			}
		}
		return out, nil
	}
	_ = r.Close()
	return nil, fmt.Errorf("sqlite driver lacks required column metadata")
}
func (r timestampRows) Next(dest []driver.Value) error {
	if err := r.sqliteRows.Next(dest); err != nil {
		return err
	}
	for _, i := range r.times {
		if s, ok := dest[i].(string); ok {
			if at, ok := timestamps.ParseLegacy(s); ok {
				dest[i] = at
			}
		}
	}
	return nil
}

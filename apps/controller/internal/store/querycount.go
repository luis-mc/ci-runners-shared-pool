package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"path/filepath"
	"sync/atomic"

	"fmt"

	"modernc.org/sqlite"
)

// QueryCount counts the statements a store ran, for tests that pin how many queries a handler makes (CRSP290: a list
// page must not ask one question per row). Like OpenForTests it is for tests; production uses Open.
type QueryCount struct{ n atomic.Int64 }

// Load is the number of statements run so far.
func (c *QueryCount) Load() int64 { return c.n.Load() }

// Reset sets the count to zero.
func (c *QueryCount) Reset() { c.n.Store(0) }

// OpenCounted is OpenForTests with every query and exec on both pools counted.
func OpenCounted(ctx context.Context, dataDir string) (*Store, *QueryCount, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create data dir: %w", err)
	}
	c := &QueryCount{}
	open := func(dsn string) (*sql.DB, error) {
		return sql.OpenDB(countConnector{dsn: dsn, drv: &sqlite.Driver{}, count: c}), nil
	}
	s, err := openPathWith(ctx, filepath.Join(dataDir, DatabaseFile), open, "synchronous(OFF)")
	return s, c, err
}

type countConnector struct {
	dsn   string
	drv   driver.Driver
	count *QueryCount
}

func (c countConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &countConn{Conn: conn, count: c.count}, nil
}

func (c countConnector) Driver() driver.Driver { return c.drv }

type countConn struct {
	driver.Conn
	count *QueryCount
}

func (c *countConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, q)
	}
	return c.Prepare(q)
}

func (c *countConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, o)
	}
	return c.Conn.Begin() //nolint:staticcheck // the inner connection has no BeginTx
}

func (c *countConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.count.n.Add(1)
	return qc.QueryContext(ctx, q, args)
}

func (c *countConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.count.n.Add(1)
	return ec.ExecContext(ctx, q, args)
}

func (c *countConn) CheckNamedValue(v *driver.NamedValue) error {
	if ck, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

func (c *countConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *countConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *countConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

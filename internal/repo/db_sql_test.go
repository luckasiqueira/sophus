package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
)

type execOnlyDriver struct {
	calls atomic.Int32
}

func (d *execOnlyDriver) Open(string) (driver.Conn, error) {
	return &execOnlyConn{calls: &d.calls}, nil
}

type execOnlyConn struct {
	calls *atomic.Int32
}

func (c *execOnlyConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}

func (c *execOnlyConn) Close() error { return nil }
func (c *execOnlyConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *execOnlyConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.calls.Add(1)
	return driver.RowsAffected(1), nil
}

func TestInsertUsesExecAndReleasesConnection(t *testing.T) {
	driverName := "repo-insert-exec-test"
	testDriver := &execOnlyDriver{}
	sql.Register(driverName, testDriver)
	testDB, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	testDB.SetMaxOpenConns(1)

	originalDB := db
	db = testDB
	t.Cleanup(func() {
		db = originalDB
		_ = testDB.Close()
	})

	for i := 0; i < 20; i++ {
		if err := insert("INSERT INTO messages (id) VALUES ($1)", i); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if got := testDriver.calls.Load(); got != 20 {
		t.Fatalf("ExecContext calls = %d, want 20", got)
	}
	if inUse := testDB.Stats().InUse; inUse != 0 {
		t.Fatalf("database connections in use = %d, want 0", inUse)
	}
}

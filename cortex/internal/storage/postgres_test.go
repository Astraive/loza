package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/astraive/loza/cortex/internal/config"
)

// errPingFailure is returned by the fake driver's Pinger to force db.Ping to
// fail after a connection has been established and returned to the idle pool.
var errPingFailure = errors.New("fake ping failure")

type pingFailDriver struct {
	closes *atomic.Int64
}

func (d *pingFailDriver) Open(string) (driver.Conn, error) {
	return &pingFailConn{closes: d.closes}, nil
}

type pingFailConn struct {
	closes *atomic.Int64
}

func (c *pingFailConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}

func (c *pingFailConn) Close() error {
	c.closes.Add(1)
	return nil
}

func (c *pingFailConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not supported")
}

// Ping implements driver.Pinger. A plain (non-ErrBadConn) error keeps the
// connection in the idle pool, so only an explicit db.Close closes it.
func (c *pingFailConn) Ping(context.Context) error {
	return errPingFailure
}

func TestNewPostgresStorageClosesPoolWhenPingFails(t *testing.T) {
	const driverName = "postgres-ping-fail-test"
	var closes atomic.Int64
	sql.Register(driverName, &pingFailDriver{closes: &closes})

	cfg := config.PostgresConfig{
		MaxConnections: 2,
		MaxIdleConns:   2,
	}

	stor, err := newPostgresStorage(driverName, "fake-dsn", cfg)
	if err == nil {
		t.Fatal("expected ping error, got nil")
	}
	if stor != nil {
		t.Fatalf("expected nil storage on ping failure, got %#v", stor)
	}
	if !errors.Is(err, errPingFailure) {
		t.Fatalf("expected wrapped ping error, got %v", err)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("expected the pooled connection to be closed once on ping failure, got %d", got)
	}
}

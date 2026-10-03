package controllers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"Go.exchange/global"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type mediaDeletionFixture struct {
	deleted, queued, pendingDeleted, pendingQueued bool
	failQueue                                      bool
	commits, rollbacks                             int
	medium, large                                  string
}
type mediaDeletionConnector struct{ fixture *mediaDeletionFixture }

func (c mediaDeletionConnector) Connect(context.Context) (driver.Conn, error) {
	return mediaDeletionConn{fixture: c.fixture}, nil
}
func (mediaDeletionConnector) Driver() driver.Driver { return visibilityRefreshDriver{} }

type mediaDeletionConn struct{ fixture *mediaDeletionFixture }

func (mediaDeletionConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (mediaDeletionConn) Close() error { return nil }
func (c mediaDeletionConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c mediaDeletionConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.fixture.pendingDeleted = c.fixture.deleted
	c.fixture.pendingQueued = c.fixture.queued
	return mediaDeletionTx{c.fixture}, nil
}

type mediaDeletionTx struct{ fixture *mediaDeletionFixture }

func (tx mediaDeletionTx) Commit() error {
	tx.fixture.deleted = tx.fixture.pendingDeleted
	tx.fixture.queued = tx.fixture.pendingQueued
	tx.fixture.commits++
	return nil
}
func (tx mediaDeletionTx) Rollback() error {
	tx.fixture.pendingDeleted = tx.fixture.deleted
	tx.fixture.pendingQueued = tx.fixture.queued
	tx.fixture.rollbacks++
	return nil
}
func (c mediaDeletionConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch {
	case strings.Contains(query, `FROM "posts"`):
		return &visibilityRefreshRows{columns: []string{"id", "author_id"}, values: [][]driver.Value{{int64(101), int64(42)}}}, nil
	case strings.Contains(query, `FROM "post_media"`):
		if !c.fixture.pendingDeleted {
			return nil, errors.New("media work was registered before Post deletion")
		}
		return &visibilityRefreshRows{columns: []string{"post_id", "url", "large_url"}, values: [][]driver.Value{{int64(101), c.fixture.medium, c.fixture.large}}}, nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}
func (c mediaDeletionConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch {
	case strings.Contains(query, `"post_reposts"`):
		return driver.RowsAffected(0), nil
	case strings.HasPrefix(query, `UPDATE "posts"`):
		c.fixture.pendingDeleted = true
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(query, `INSERT INTO "post_media_cleanup"`):
		if c.fixture.failQueue {
			return nil, errors.New("cleanup queue unavailable")
		}
		c.fixture.pendingQueued = true
		return driver.RowsAffected(1), nil
	default:
		return nil, errors.New("unexpected exec: " + query)
	}
}

func TestPostMediaDeletionAndCleanupRegistrationCommitTogether(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []string{"success", "queue failure", "wrong owner metadata", "DevData retained"} {
		t.Run(test, func(t *testing.T) {
			fixture := &mediaDeletionFixture{medium: "/api/files/post-media/users/v1/42/550e8400-e29b-41d4-a716-446655440000/medium.jpg", large: "/api/files/post-media/users/v1/42/550e8400-e29b-41d4-a716-446655440000/large.jpg"}
			switch test {
			case "queue failure":
				fixture.failQueue = true
			case "wrong owner metadata":
				fixture.medium = strings.Replace(fixture.medium, "/42/", "/43/", 1)
			case "DevData retained":
				fixture.medium = "/api/files/post-media/devdata/v1/test/1/" + strings.Repeat("a", 64) + "/medium.jpg"
				fixture.large = strings.Replace(fixture.medium, "medium.jpg", "large.jpg", 1)
			}
			connection := sql.OpenDB(mediaDeletionConnector{fixture: fixture})
			t.Cleanup(func() { _ = connection.Close() })
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			previous := global.APIDb
			global.APIDb = db
			t.Cleanup(func() { global.APIDb = previous })
			_, err = deletePostInTransactionFromDB(context.Background(), 101, 42)
			if test == "success" || test == "DevData retained" {
				if err != nil || !fixture.deleted || fixture.commits != 1 || fixture.queued != (test == "success") {
					t.Fatalf("fixture=%+v err=%v", fixture, err)
				}
			} else if err == nil || fixture.deleted || fixture.queued || fixture.rollbacks != 1 {
				t.Fatalf("partial deletion committed: fixture=%+v err=%v", fixture, err)
			}
		})
	}
}

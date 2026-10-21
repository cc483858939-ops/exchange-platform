package recommendation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type semanticRecallQueryRecorder struct {
	count int
	query string
	vars  []driver.NamedValue
}

type semanticRecallQueryConnector struct{ recorder *semanticRecallQueryRecorder }

func (connector semanticRecallQueryConnector) Connect(context.Context) (driver.Conn, error) {
	return semanticRecallQueryConn{recorder: connector.recorder}, nil
}

func (connector semanticRecallQueryConnector) Driver() driver.Driver {
	return semanticRecallQueryDriver{recorder: connector.recorder}
}

type semanticRecallQueryDriver struct{ recorder *semanticRecallQueryRecorder }

func (driver semanticRecallQueryDriver) Open(string) (driver.Conn, error) {
	return semanticRecallQueryConn{recorder: driver.recorder}, nil
}

type semanticRecallQueryConn struct{ recorder *semanticRecallQueryRecorder }

func (semanticRecallQueryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are unsupported in semantic query test")
}

func (semanticRecallQueryConn) Close() error { return nil }

func (semanticRecallQueryConn) Begin() (driver.Tx, error) {
	return semanticRecallQueryTx{}, nil
}

func (connection semanticRecallQueryConn) QueryContext(_ context.Context, query string, vars []driver.NamedValue) (driver.Rows, error) {
	connection.recorder.count++
	connection.recorder.query = query
	connection.recorder.vars = append([]driver.NamedValue(nil), vars...)
	return semanticRecallEmptyRows{}, nil
}

type semanticRecallQueryTx struct{}

func (semanticRecallQueryTx) Commit() error   { return nil }
func (semanticRecallQueryTx) Rollback() error { return nil }

type semanticRecallEmptyRows struct{}

func (semanticRecallEmptyRows) Columns() []string {
	return []string{"post_id", "positive_semantic_similarity"}
}
func (semanticRecallEmptyRows) Close() error { return nil }
func (semanticRecallEmptyRows) Next([]driver.Value) error {
	return io.EOF
}

func TestSemanticRecallBuildsOneWindowScopedCandidateQuery(t *testing.T) {
	recorder := &semanticRecallQueryRecorder{}
	sqlDB := sql.OpenDB(semanticRecallQueryConnector{recorder: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	query := CandidateQuery{
		UserID: 73, ServingVersion: "serving-v2", Limit: 3, PositiveVector: []float32{1, 0},
		CandidateWindowStart: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
		CandidateWindowEnd:   time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
	}
	if _, err := repository.LoadSemanticCandidates(context.Background(), query); err != nil {
		t.Fatalf("build semantic candidate query: %v", err)
	}
	if recorder.count != 1 {
		t.Fatalf("semantic candidate query count=%d, want one", recorder.count)
	}
	sqlText := strings.Join(strings.Fields(recorder.query), " ")
	for _, fragment := range []string{
		"FROM post_embeddings AS ae",
		"ae.version =",
		"ae.dimensions =",
		"ae.dimensions > 0",
		"vector_norm(ae.embedding) > 0",
		"posts.created_at >=",
		"posts.created_at <=",
		"ORDER BY ae.embedding <=>",
		"LIMIT",
	} {
		if !strings.Contains(sqlText, fragment) {
			t.Errorf("semantic candidate SQL is missing %q: %s", fragment, sqlText)
		}
	}
	if strings.Count(sqlText, "LIMIT") != 1 {
		t.Fatalf("semantic candidate SQL has multiple limits: %s", sqlText)
	}
	var sawServingVersion bool
	for _, variable := range recorder.vars {
		if variable.Value == query.ServingVersion {
			sawServingVersion = true
		}
	}
	if !sawServingVersion {
		t.Fatalf("semantic recall query did not bind only the active serving version %q: %#v", query.ServingVersion, recorder.vars)
	}
}

func TestSemanticRecallSkipsMissingPositiveProfileVector(t *testing.T) {
	recorder := &semanticRecallQueryRecorder{}
	sqlDB := sql.OpenDB(semanticRecallQueryConnector{recorder: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open PostgreSQL database: %v", err)
	}
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range [][]float32{nil, {}, {0, 0}} {
		if candidates, err := repository.LoadSemanticCandidates(context.Background(), CandidateQuery{
			ServingVersion: "serving-v2", Limit: 10, PositiveVector: vector,
		}); err != nil || len(candidates) != 0 {
			t.Fatalf("semantic candidates=%v err=%v for invalid profile vector %v", candidates, err, vector)
		}
	}
	if recorder.count != 0 {
		t.Fatalf("semantic queries=%d, want no SQL without a valid positive profile vector", recorder.count)
	}
}

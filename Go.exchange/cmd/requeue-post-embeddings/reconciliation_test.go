package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/eventing"
)

type reconciliationTestScanner struct {
	pages [][]requeuePost
	err   error
	calls []struct {
		lastID   uint
		pageSize int
	}
}

func (s *reconciliationTestScanner) ListPage(_ context.Context, lastID uint, pageSize int) ([]requeuePost, error) {
	s.calls = append(s.calls, struct {
		lastID   uint
		pageSize int
	}{lastID: lastID, pageSize: pageSize})
	if s.err != nil {
		return nil, s.err
	}
	if len(s.pages) == 0 {
		return nil, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

type reconciliationTestPublisher struct {
	events []eventing.Envelope
	err    error
	calls  int
}

func (p *reconciliationTestPublisher) PublishBatch(_ context.Context, events []eventing.Envelope) error {
	p.calls++
	if p.err != nil {
		return p.err
	}
	p.events = append(p.events, events...)
	return nil
}

func reconciliationPost(id uint, title, content string, version, hash *string) requeuePost {
	return requeuePost{
		ID: id, Title: title, Content: content,
		EmbeddingPostID: func() *uint {
			if version == nil && hash == nil {
				return nil
			}
			value := id
			return &value
		}(),
		EmbeddingVersion: version, EmbeddingContentHash: hash,
	}
}

func stringPointer(value string) *string { return &value }

func TestReconcilePostEmbeddingsClassifiesAndPublishesOnlyStaleRows(t *testing.T) {
	currentHash := embeddings.PostEmbeddingContentHash("current", "", "body")
	scanner := &reconciliationTestScanner{pages: [][]requeuePost{{
		reconciliationPost(1, "missing", "body", nil, nil),
		reconciliationPost(2, "current", "body", stringPointer("v1"), stringPointer(currentHash)),
		reconciliationPost(3, "both stale", "body", stringPointer("old"), stringPointer("old-hash")),
		reconciliationPost(4, "content stale", "body", stringPointer("v1"), stringPointer("old-hash")),
	}, nil}}
	publisher := &reconciliationTestPublisher{}

	stats, err := reconcilePostEmbeddings(context.Background(), scanner, publisher, "v1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scanned != 4 || stats.Missing != 1 || stats.StaleVersion != 1 || stats.StaleContent != 1 || stats.Published != 3 {
		t.Fatalf("stats=%+v", stats)
	}
	if publisher.calls != 1 || len(publisher.events) != 3 {
		t.Fatalf("publisher calls=%d events=%d", publisher.calls, len(publisher.events))
	}
}

func TestReconcilePostEmbeddingsPaginationAdvancesWithoutDuplicates(t *testing.T) {
	first := make([]requeuePost, 500)
	for index := range first {
		id := uint(index + 1)
		first[index] = reconciliationPost(id, "title", "body", nil, nil)
	}
	second := []requeuePost{reconciliationPost(501, "title", "body", nil, nil)}
	scanner := &reconciliationTestScanner{pages: [][]requeuePost{first, second, nil}}
	publisher := &reconciliationTestPublisher{}

	stats, err := reconcilePostEmbeddings(context.Background(), scanner, publisher, "v1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scanned != 501 || stats.Published != 501 || len(publisher.events) != 501 {
		t.Fatalf("stats=%+v events=%d", stats, len(publisher.events))
	}
	if len(scanner.calls) != 3 || scanner.calls[0].lastID != 0 || scanner.calls[1].lastID != 500 || scanner.calls[2].lastID != 501 {
		t.Fatalf("calls=%+v", scanner.calls)
	}
	for _, call := range scanner.calls {
		if call.pageSize != requeuePostEmbeddingPageSize {
			t.Fatalf("page size=%d", call.pageSize)
		}
	}
	seen := make(map[string]struct{}, len(publisher.events))
	for _, event := range publisher.events {
		if _, exists := seen[event.AggregateID]; exists {
			t.Fatalf("duplicate post id=%s", event.AggregateID)
		}
		seen[event.AggregateID] = struct{}{}
	}
}

func TestReconcilePostEmbeddingsPublishAndScannerFailures(t *testing.T) {
	t.Run("publish", func(t *testing.T) {
		publisherErr := errors.New("broker down")
		scanner := &reconciliationTestScanner{pages: [][]requeuePost{{reconciliationPost(1, "title", "body", nil, nil)}}}
		_, err := reconcilePostEmbeddings(context.Background(), scanner, &reconciliationTestPublisher{err: publisherErr}, "v1", time.Now().UTC())
		if !errors.Is(err, publisherErr) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("scan", func(t *testing.T) {
		scannerErr := errors.New("scan failed")
		scanner := &reconciliationTestScanner{err: scannerErr}
		_, err := reconcilePostEmbeddings(context.Background(), scanner, &reconciliationTestPublisher{}, "v1", time.Now().UTC())
		if !errors.Is(err, scannerErr) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		scanner := &reconciliationTestScanner{pages: [][]requeuePost{nil}}
		stats, err := reconcilePostEmbeddings(context.Background(), scanner, nil, "v1", time.Now().UTC())
		if err != nil || stats != (requeueStats{}) {
			t.Fatalf("stats=%+v err=%v", stats, err)
		}
	})
}

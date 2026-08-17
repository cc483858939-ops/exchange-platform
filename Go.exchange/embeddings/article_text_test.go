package embeddings

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestBuildArticleEmbeddingTextUsesOnlyTitleAndContent(t *testing.T) {
	if got := BuildArticleEmbeddingText(" Title ", "Body"); got != " Title \n\nBody" {
		t.Fatalf("text=%q", got)
	}
	if got := BuildArticleEmbeddingText("", "Body"); got != "Body" {
		t.Fatalf("empty title text=%q", got)
	}
}

func TestArticleEmbeddingContentHashIsCanonicalSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("title\n\nbody"))
	if got, want := ArticleEmbeddingContentHash("title", "body"), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("hash=%q want=%q", got, want)
	}
}

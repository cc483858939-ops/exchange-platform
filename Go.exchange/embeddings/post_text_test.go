package embeddings

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestBuildPostEmbeddingTextIncludesTitlePreviewAndContent(t *testing.T) {
	if got := BuildPostEmbeddingText(" Title ", "Preview", "Body"); got != " Title \n\nPreview\n\nBody" {
		t.Fatalf("text=%q", got)
	}
	if got := BuildPostEmbeddingText("", "", "Body"); got != "\n\n\n\nBody" {
		t.Fatalf("empty title text=%q", got)
	}
}

func TestPostEmbeddingContentHashIsCanonicalSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("title\n\npreview\n\nbody"))
	if got, want := PostEmbeddingContentHash("title", "preview", "body"), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("hash=%q want=%q", got, want)
	}
}

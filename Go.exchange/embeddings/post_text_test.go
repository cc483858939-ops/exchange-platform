package embeddings

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestBuildPostEmbeddingTextUsesCanonicalContent(t *testing.T) {
	if got := BuildPostEmbeddingText("Body"); got != "Body" {
		t.Fatalf("text=%q", got)
	}
}

func TestPostEmbeddingContentHashIsCanonicalSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("Body"))
	if got, want := PostEmbeddingContentHash("Body"), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("hash=%q want=%q", got, want)
	}
}

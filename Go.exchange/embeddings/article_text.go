package embeddings

import "crypto/sha256"

// BuildPostEmbeddingText returns the canonical title, preview, and Post body
// text used for every Post embedding, including short Posts without metadata.
func BuildPostEmbeddingText(title, preview, content string) string {
	return title + "\n\n" + preview + "\n\n" + content
}

// PostEmbeddingContentHash returns the SHA-256 hash of canonical embedding text.
func PostEmbeddingContentHash(title, preview, content string) string {
	sum := sha256.Sum256([]byte(BuildPostEmbeddingText(title, preview, content)))
	return stringHex(sum[:])
}

func stringHex(value []byte) string {
	const hex = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for i, b := range value {
		result[i*2] = hex[b>>4]
		result[i*2+1] = hex[b&0x0f]
	}
	return string(result)
}

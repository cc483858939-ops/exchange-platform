package embeddings

import "context"

type Embedder interface {
	Embed(ctx context.Context, texts []string) (EmbedResult, error)
}

type EmbedResult struct {
	Vectors [][]float32
	Model   string
}

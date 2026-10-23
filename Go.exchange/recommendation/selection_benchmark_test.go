package recommendation

import (
	"math/rand"
	"testing"
	"time"

	"Go.exchange/models"

	"gorm.io/gorm"
)

const selectionBenchmarkEmbeddingDimensions = 1024 // BGE-M3's production vector size.

var selectionBenchmarkSink []SelectedCandidate

type selectionBenchmarkScenario struct {
	name  string
	input SelectionInput
}

func benchmarkEmbedding(rng *rand.Rand, similarTo []float32) []float32 {
	vector := make([]float32, selectionBenchmarkEmbeddingDimensions)
	for index := range vector {
		if similarTo != nil {
			vector[index] = similarTo[index] + (rng.Float32()-0.5)*0.01
			continue
		}
		vector[index] = rng.Float32()*2 - 1
	}
	return vector
}

func benchmarkSelectedCandidate(id, authorID uint, embedding []float32, now time.Time) SelectedCandidate {
	return SelectedCandidate{
		Candidate: Candidate{PostID: id},
		Post: models.Post{
			Model:    gorm.Model{ID: id, CreatedAt: now.Add(-time.Hour)},
			AuthorID: authorID,
		},
		Embedding: embedding,
	}
}

func newSelectionBenchmarkScenario(
	name string,
	candidateCount int,
	pageSize int,
	priorCount int,
	similarEmbeddings bool,
	repeatedAuthors bool,
	exploration bool,
) selectionBenchmarkScenario {
	const seed = int64(230023)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(seed))
	var similarTo []float32
	if similarEmbeddings {
		similarTo = benchmarkEmbedding(rng, nil)
	}

	candidates := make([]RankedCandidate, candidateCount)
	for index := range candidates {
		id := uint(index + 1)
		authorID := uint(index%80 + 1)
		if repeatedAuthors {
			authorID = uint(index%4 + 1)
		}
		fromRecent := exploration
		candidates[index] = RankedCandidate{
			Candidate: Candidate{
				PostID: id, FromRecent: fromRecent,
			},
			Post: models.Post{
				Model:    gorm.Model{ID: id, CreatedAt: now.Add(-time.Duration(index%24+1) * time.Hour)},
				AuthorID: authorID,
			},
			Embedding:           benchmarkEmbedding(rng, similarTo),
			Breakdown:           ScoreBreakdown{BaseScore: float64(candidateCount - index)},
			ExplorationSemantic: float64(candidateCount-index) / float64(candidateCount),
			IsInNetwork:         index%10 < 7,
			IsNovelAuthor:       exploration && index%7 == 0,
		}
	}

	prior := make([]SelectedCandidate, priorCount)
	for index := range prior {
		authorID := uint(index%80 + 1)
		if repeatedAuthors {
			authorID = uint(index%4 + 1)
		}
		prior[index] = benchmarkSelectedCandidate(
			uint(10_000+index), authorID, benchmarkEmbedding(rng, similarTo), now,
		)
	}

	selectionConfig := testSelectionConfig(DefaultConfig())
	if !exploration {
		selectionConfig.Exploration.Ratio = 0
	}
	return selectionBenchmarkScenario{
		name: name,
		input: SelectionInput{
			Candidates: candidates,
			Prior:      prior,
			Limit:      pageSize,
			Now:        now,
			Phase:      SelectionPhaseFresh,
			RequestID:  "a23-selection-benchmark-request",
			Config:     selectionConfig,
		},
	}
}

func BenchmarkSelectCandidates(b *testing.B) {
	scenarios := []selectionBenchmarkScenario{
		newSelectionBenchmarkScenario("B01_50_candidates_page_20", 50, 20, 0, false, false, false),
		newSelectionBenchmarkScenario("B02_200_candidates_page_20", 200, 20, 0, false, false, false),
		newSelectionBenchmarkScenario("B03_500_candidates_page_20", 500, 20, 0, false, false, false),
		newSelectionBenchmarkScenario("B04_500_candidates_page_50", 500, 50, 0, false, false, false),
		newSelectionBenchmarkScenario("B05_500_candidates_similar_embeddings", 500, 20, 0, true, false, false),
		newSelectionBenchmarkScenario("B06_500_candidates_prior_200", 500, 20, 200, false, false, false),
		newSelectionBenchmarkScenario("B07_500_candidates_author_window_fallback", 500, 20, 8, false, true, false),
		newSelectionBenchmarkScenario("B08_500_candidates_exploration", 500, 20, 0, false, false, true),
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				selectionBenchmarkSink = SelectCandidates(scenario.input)
			}
		})
	}
}

package recommendation

import (
	"fmt"
	"testing"

	"Go.exchange/models"
)

func TestTraceDispatcherRejectedJobsDoNotAllocateResultCopies(t *testing.T) {
	for _, accepting := range []bool{true, false} {
		dispatcher := &AsyncTraceDispatcher{
			jobs: make(chan TracePersistJob, 1), metrics: NoopMetrics{}, accepting: accepting,
		}
		dispatcher.jobs <- TracePersistJob{}
		job := TracePersistJob{Results: make([]models.RecommendationResultTrace, 100)}
		want := TraceEnqueueDroppedStopping
		if accepting {
			want = TraceEnqueueDroppedFull
		}
		allocations := testing.AllocsPerRun(50, func() {
			if got := dispatcher.TryEnqueue(job); got != want {
				t.Fatalf("enqueue=%s want=%s", got, want)
			}
		})
		if allocations != 0 {
			t.Fatalf("rejected job allocated %.0f objects", allocations)
		}
	}
}

func BenchmarkTraceDispatcherRejectedJob(b *testing.B) {
	for _, count := range []int{20, 100} {
		for _, stopping := range []bool{false, true} {
			b.Run(fmt.Sprintf("results_%d_stopping_%t", count, stopping), func(b *testing.B) {
				dispatcher := &AsyncTraceDispatcher{
					jobs: make(chan TracePersistJob, 1), metrics: NoopMetrics{}, accepting: !stopping,
				}
				dispatcher.jobs <- TracePersistJob{}
				job := TracePersistJob{Results: make([]models.RecommendationResultTrace, count)}
				want := TraceEnqueueDroppedFull
				if stopping {
					want = TraceEnqueueDroppedStopping
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if got := dispatcher.TryEnqueue(job); got != want {
						b.Fatalf("enqueue=%s want=%s", got, want)
					}
				}
			})
		}
	}
}

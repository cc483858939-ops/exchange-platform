package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"strconv"
	"time"
)

var (
	registry            = prometheus.NewRegistry()
	httpRequestsTotal   = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "go_exchange_http_requests_total", Help: "Total number of HTTP requests handled by the Gin server."}, []string{"method", "route", "status"})
	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "go_exchange_http_request_duration_seconds", Help: "HTTP request latency in seconds.", Buckets: prometheus.DefBuckets}, []string{"method", "route", "status"})
	articleAnalysisJobs = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_article_analysis_jobs", Help: "Current durable article analysis jobs by state."}, []string{"state"})
	outboxPending       = prometheus.NewGauge(prometheus.GaugeOpts{Name: "go_exchange_outbox_pending_events", Help: "Current unpublished outbox event count."})
	likePipelineDepth   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "go_exchange_like_pipeline_depth", Help: "Current Redis like pipeline depth by stage."}, []string{"stage"})
)

func init() {
	registry.MustRegister(httpRequestsTotal, httpRequestDuration, articleAnalysisJobs, outboxPending, likePipelineDepth)
}
func Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.Path == "/metrics" {
			ctx.Next()
			return
		}
		started := time.Now()
		ctx.Next()
		route := ctx.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(ctx.Writer.Status())
		httpRequestsTotal.WithLabelValues(ctx.Request.Method, route, status).Inc()
		httpRequestDuration.WithLabelValues(ctx.Request.Method, route, status).Observe(time.Since(started).Seconds())
	}
}
func Handler() http.Handler { return promhttp.HandlerFor(registry, promhttp.HandlerOpts{}) }
func SetArticleAnalysisJobs(state string, value float64) {
	articleAnalysisJobs.WithLabelValues(state).Set(value)
}
func SetOutboxPending(value float64) { outboxPending.Set(value) }
func SetLikePipelineDepth(stage string, value float64) {
	likePipelineDepth.WithLabelValues(stage).Set(value)
}

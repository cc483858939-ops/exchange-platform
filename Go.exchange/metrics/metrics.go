// Package metrics 提供了基于 Prometheus 的指标监控功能，用于追踪系统性能和运行状态。
package metrics

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// registry 创建一个私有的 Prometheus 注册表，避免污染全局默认注册表。
	registry = prometheus.NewRegistry()

	// httpRequestsTotal 统计 Gin 服务器处理的 HTTP 请求总数，包含方法、路径和状态码标签。
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "go_exchange_http_requests_total",
			Help: "Total number of HTTP requests handled by the Gin server.",
		},
		[]string{"method", "route", "status"},
	)
	// httpRequestDuration 记录 HTTP 请求的延迟时间（秒），用于分析接口性能。
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "go_exchange_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route", "status"},
	)
	// tasksDirtyBacklog 监控 Redis 中待持久化的“点赞”等任务的积压长度。
	tasksDirtyBacklog = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_exchange_tasks_dirty_backlog",
			Help: "Current Redis dirty backlog length for like persistence.",
		},
	)
	// dynamicWorkersFunc 存储一个动态获取当前活跃工作协程数量的函数。
	dynamicWorkersFunc atomic.Value
)

func init() {
	// 初始化时设置默认的动态工作协程获取函数。
	SetDynamicWorkersFunc(nil)

	// 将定义的指标注册到私有注册表中。
	registry.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		tasksDirtyBacklog,
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Name: "go_exchange_tasks_dynamic_workers",
				Help: "Current number of active dynamic task workers.",
			},
			func() float64 {
				fn, _ := dynamicWorkersFunc.Load().(func() float64)
				if fn == nil {
					return 0
				}
				return fn()
			},
		),
	)
}

// Middleware 返回一个 Gin 中间件，用于自动采集每个请求的吞吐量和延迟指标。
func Middleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// 跳过对 /metrics 路径自身的统计，避免监控数据污染。
		if ctx.Request.URL.Path == "/metrics" {
			ctx.Next()
			return
		}

		start := time.Now()
		ctx.Next()

		// 获取匹配的路由模板（如 /user/:id），若未匹配则标记为 unmatched。
		route := ctx.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(ctx.Writer.Status())

		// 更新请求总数和请求延迟指标。
		httpRequestsTotal.WithLabelValues(ctx.Request.Method, route, status).Inc()
		httpRequestDuration.WithLabelValues(ctx.Request.Method, route, status).Observe(time.Since(start).Seconds())
	}
}

// Handler 返回 Prometheus 指标的 HTTP 处理器，供监控系统抓取数据。
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// SetDynamicWorkersFunc 设置用于动态计算活跃工作协程数量的钩子函数。
func SetDynamicWorkersFunc(fn func() float64) {
	if fn == nil {
		fn = func() float64 { return 0 }
	}
	dynamicWorkersFunc.Store(fn)
}

// SetDirtyBacklog 手动设置当前任务积压的数值。
func SetDirtyBacklog(value float64) {
	tasksDirtyBacklog.Set(value)
}

package server

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

// httpMetrics 记录请求级指标：
//   - filegate_http_requests_total{method,path,status}        请求总数（QPS/错误率）
//   - filegate_http_request_duration_seconds{method,path}     请求耗时分布（P50/P99）
//
// 标签只取有限值：namespace/class 来自配置，path 用路由模板。
// objectPath 基数无界，不用作标签。
type httpMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func newHTTPMetrics(reg prometheus.Registerer) *httpMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &httpMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "filegate",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests processed, partitioned by method, route, namespace, class and status code.",
		}, []string{"method", "path", "namespace", "class", "status"}),

		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "filegate",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds, partitioned by method, route, namespace and class.",
			// bucket 上限放到 60s，覆盖大文件下载
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"method", "path", "namespace", "class"}),
	}

	reg.MustRegister(m.requestsTotal)
	reg.MustRegister(m.requestDuration)
	return m
}

func (m *httpMetrics) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath() // 未匹配路由时为空
		if path == "" {
			path = "unrouted"
		}
		ns := c.Param("namespace") // 未路由时 Param 为空，兜底避免空标签
		if ns == "" {
			ns = "-"
		}
		class := c.Param("class")
		if class == "" {
			class = "-"
		}

		m.requestsTotal.WithLabelValues(c.Request.Method, path, ns, class, strconv.Itoa(c.Writer.Status())).Inc()
		m.requestDuration.WithLabelValues(c.Request.Method, path, ns, class).Observe(time.Since(start).Seconds())
	}
}

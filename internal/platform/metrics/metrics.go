// Package metrics 提供进程级和 HTTP 级 Prometheus 指标。
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Registry struct {
	registry        *prometheus.Registry
	httpRequests    *prometheus.CounterVec
	httpDuration    *prometheus.HistogramVec
	httpRequestsNow prometheus.Gauge
}

func NewRegistry() *Registry {
	registry := prometheus.NewRegistry()
	metrics := &Registry{
		registry: registry,
		httpRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system",
				Subsystem: "http",
				Name:      "requests_total",
				Help:      "Total number of HTTP requests.",
			},
			[]string{"method", "route", "status"},
		),
		httpDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "order_system",
				Subsystem: "http",
				Name:      "request_duration_seconds",
				Help:      "HTTP request duration in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method", "route"},
		),
		httpRequestsNow: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "order_system",
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "Current number of HTTP requests being processed.",
		}),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.httpRequestsNow,
	)
	return metrics
}

func (r *Registry) RequestStarted() {
	r.httpRequestsNow.Inc()
}

func (r *Registry) RequestFinished(
	method string,
	route string,
	statusCode int,
	duration time.Duration,
) {
	r.httpRequestsNow.Dec()
	r.httpRequests.WithLabelValues(method, route, strconv.Itoa(statusCode)).Inc()
	r.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

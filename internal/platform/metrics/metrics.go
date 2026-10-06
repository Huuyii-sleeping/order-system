// Package metrics 提供进程、HTTP 和消息链路的 Prometheus 指标。
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
	registry                *prometheus.Registry
	httpRequests            *prometheus.CounterVec
	httpDuration            *prometheus.HistogramVec
	httpRequestsNow         prometheus.Gauge
	kafkaProcessingAttempts *prometheus.CounterVec
	kafkaRetries            *prometheus.CounterVec
	kafkaDeadLetters        *prometheus.CounterVec
	kafkaOffsetCommits      *prometheus.CounterVec
	kafkaConsumerLag        *prometheus.GaugeVec
	catalogCacheOperations  *prometheus.CounterVec
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
		kafkaProcessingAttempts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "kafka",
				Name: "processing_attempts_total",
				Help: "Total number of Kafka record processing attempts by outcome.",
			},
			[]string{"consumer_group", "topic", "outcome"},
		),
		kafkaRetries: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "kafka",
				Name: "retries_total",
				Help: "Total number of Kafka record retries scheduled by reason.",
			},
			[]string{"consumer_group", "topic", "reason"},
		),
		kafkaDeadLetters: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "kafka",
				Name: "dead_letters_published_total",
				Help: "Total number of records successfully published to a dead letter topic.",
			},
			[]string{"consumer_group", "topic"},
		),
		kafkaOffsetCommits: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "kafka",
				Name: "offset_commits_total",
				Help: "Total number of synchronous Kafka offset commit attempts by outcome.",
			},
			[]string{"consumer_group", "topic", "outcome"},
		),
		kafkaConsumerLag: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "order_system", Subsystem: "kafka",
				Name: "consumer_lag_records",
				Help: "Kafka consumer lag from the latest fetch high watermark snapshot.",
			},
			[]string{"consumer_group", "topic", "partition"},
		),
		catalogCacheOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "catalog_cache",
				Name: "operations_total",
				Help: "Total number of catalog cache operations by bounded outcome.",
			},
			[]string{"outcome"},
		),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.httpRequestsNow,
		metrics.kafkaProcessingAttempts,
		metrics.kafkaRetries,
		metrics.kafkaDeadLetters,
		metrics.kafkaOffsetCommits,
		metrics.kafkaConsumerLag,
		metrics.catalogCacheOperations,
	)
	return metrics
}

func (r *Registry) RecordCatalogCacheOperation(outcome string) {
	r.catalogCacheOperations.WithLabelValues(outcome).Inc()
}

func (r *Registry) RecordProcessingAttempt(consumerGroup, topic, outcome string) {
	r.kafkaProcessingAttempts.WithLabelValues(consumerGroup, topic, outcome).Inc()
}

func (r *Registry) RecordRetry(consumerGroup, topic, reason string) {
	r.kafkaRetries.WithLabelValues(consumerGroup, topic, reason).Inc()
}

func (r *Registry) RecordDeadLetter(consumerGroup, topic string) {
	r.kafkaDeadLetters.WithLabelValues(consumerGroup, topic).Inc()
}

func (r *Registry) RecordOffsetCommit(consumerGroup, topic, outcome string) {
	r.kafkaOffsetCommits.WithLabelValues(consumerGroup, topic, outcome).Inc()
}

func (r *Registry) SetConsumerLag(consumerGroup, topic string, partition int32, lag int64) {
	if lag < 0 {
		lag = 0
	}
	r.kafkaConsumerLag.WithLabelValues(
		consumerGroup, topic, strconv.FormatInt(int64(partition), 10),
	).Set(float64(lag))
}

func (r *Registry) DeleteConsumerLag(consumerGroup, topic string, partition int32) {
	r.kafkaConsumerLag.DeleteLabelValues(
		consumerGroup, topic, strconv.FormatInt(int64(partition), 10),
	)
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

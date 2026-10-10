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
	registry                    *prometheus.Registry
	httpRequests                *prometheus.CounterVec
	httpDuration                *prometheus.HistogramVec
	httpRequestsNow             prometheus.Gauge
	kafkaProcessingAttempts     *prometheus.CounterVec
	kafkaRetries                *prometheus.CounterVec
	kafkaDeadLetters            *prometheus.CounterVec
	kafkaOffsetCommits          *prometheus.CounterVec
	kafkaConsumerLag            *prometheus.GaugeVec
	catalogCacheOperations      *prometheus.CounterVec
	paymentProviderRequests     *prometheus.CounterVec
	paymentProviderDuration     *prometheus.HistogramVec
	paymentProviderRejected     *prometheus.CounterVec
	paymentCircuitState         *prometheus.GaugeVec
	paymentCircuitChanges       *prometheus.CounterVec
	notificationTasks           *prometheus.CounterVec
	notificationRetries         *prometheus.CounterVec
	notificationReconciliations *prometheus.CounterVec
	notificationWebhooks        *prometheus.CounterVec
	authCredentialCleanupRuns   *prometheus.CounterVec
	authCredentialCleanupRows   *prometheus.CounterVec
	authCredentialCleanupTime   *prometheus.HistogramVec
	authCredentialLastSuccess   prometheus.Gauge
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
		paymentProviderRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "payment_provider",
				Name: "requests_total",
				Help: "Total number of payment provider calls by bounded outcome.",
			},
			[]string{"outcome"},
		),
		paymentProviderDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "order_system", Subsystem: "payment_provider",
				Name:    "request_duration_seconds",
				Help:    "Payment provider call duration in seconds by bounded outcome.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"outcome"},
		),
		paymentProviderRejected: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "payment_provider",
				Name: "rejections_total",
				Help: "Total number of payment provider calls rejected before reaching the dependency.",
			},
			[]string{"reason"},
		),
		paymentCircuitState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "order_system", Subsystem: "payment_provider",
				Name: "circuit_state",
				Help: "Current payment provider circuit state as one-hot gauges.",
			},
			[]string{"state"},
		),
		paymentCircuitChanges: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "payment_provider",
				Name: "circuit_transitions_total",
				Help: "Total number of payment provider circuit state transitions.",
			},
			[]string{"to"},
		),
		notificationTasks: prometheus.NewCounterVec(
			prometheus.CounterOpts{Namespace: "order_system", Subsystem: "notification", Name: "tasks_total", Help: "Total notification task outcomes."},
			[]string{"outcome"},
		),
		notificationRetries: prometheus.NewCounterVec(
			prometheus.CounterOpts{Namespace: "order_system", Subsystem: "notification", Name: "retries_total", Help: "Total notification task retries by reason."},
			[]string{"reason"},
		),
		notificationReconciliations: prometheus.NewCounterVec(
			prometheus.CounterOpts{Namespace: "order_system", Subsystem: "notification", Name: "reconciliations_total", Help: "Total notification provider reconciliation lookups by outcome."},
			[]string{"outcome"},
		),
		notificationWebhooks: prometheus.NewCounterVec(
			prometheus.CounterOpts{Namespace: "order_system", Subsystem: "notification", Name: "webhooks_total", Help: "Total notification provider webhook requests by bounded outcome."},
			[]string{"outcome"},
		),
		authCredentialCleanupRuns: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "auth_credential_cleanup",
				Name: "runs_total", Help: "Total authentication credential cleanup runs by bounded outcome.",
			},
			[]string{"outcome"},
		),
		authCredentialCleanupRows: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "order_system", Subsystem: "auth_credential_cleanup",
				Name: "deleted_rows_total", Help: "Total expired authentication credential rows deleted by kind.",
			},
			[]string{"kind"},
		),
		authCredentialCleanupTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "order_system", Subsystem: "auth_credential_cleanup",
				Name: "duration_seconds", Help: "Authentication credential cleanup duration in seconds by bounded outcome.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"outcome"},
		),
		authCredentialLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "order_system", Subsystem: "auth_credential_cleanup",
			Name: "last_success_timestamp_seconds",
			Help: "Unix timestamp of the most recent successful authentication credential cleanup.",
		}),
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
		metrics.paymentProviderRequests,
		metrics.paymentProviderDuration,
		metrics.paymentProviderRejected,
		metrics.paymentCircuitState,
		metrics.paymentCircuitChanges,
		metrics.notificationTasks,
		metrics.notificationRetries,
		metrics.notificationReconciliations,
		metrics.notificationWebhooks,
		metrics.authCredentialCleanupRuns,
		metrics.authCredentialCleanupRows,
		metrics.authCredentialCleanupTime,
		metrics.authCredentialLastSuccess,
	)
	return metrics
}

func (r *Registry) RecordNotificationTask(outcome string) {
	r.notificationTasks.WithLabelValues(outcome).Inc()
}

func (r *Registry) RecordNotificationRetry(reason string) {
	r.notificationRetries.WithLabelValues(reason).Inc()
}

func (r *Registry) RecordNotificationReconciliation(outcome string) {
	r.notificationReconciliations.WithLabelValues(outcome).Inc()
}

func (r *Registry) RecordNotificationWebhook(outcome string) {
	r.notificationWebhooks.WithLabelValues(outcome).Inc()
}

// RecordAuthCredentialCleanup 记录认证凭证维护的低基数指标。
// outcome 只允许 success、error、canceled 等固定结果；不把 token hash、用户 ID
// 或数据库错误文本放入标签，避免 Prometheus 时间序列无限增长。
func (r *Registry) RecordAuthCredentialCleanup(
	outcome string, sessionsDeleted, refreshTokensDeleted int64, duration time.Duration,
) {
	r.authCredentialCleanupRuns.WithLabelValues(outcome).Inc()
	r.authCredentialCleanupTime.WithLabelValues(outcome).Observe(duration.Seconds())
	if outcome != "success" {
		return
	}
	if sessionsDeleted > 0 {
		r.authCredentialCleanupRows.WithLabelValues("sessions").Add(float64(sessionsDeleted))
	}
	if refreshTokensDeleted > 0 {
		r.authCredentialCleanupRows.WithLabelValues("refresh_tokens").Add(float64(refreshTokensDeleted))
	}
	r.authCredentialLastSuccess.Set(float64(time.Now().Unix()))
}

func (r *Registry) RecordPaymentProviderCall(outcome string, duration time.Duration) {
	r.paymentProviderRequests.WithLabelValues(outcome).Inc()
	r.paymentProviderDuration.WithLabelValues(outcome).Observe(duration.Seconds())
}

func (r *Registry) RecordPaymentProviderRejection(reason string) {
	r.paymentProviderRejected.WithLabelValues(reason).Inc()
}

func (r *Registry) SetPaymentProviderCircuitState(state string) {
	for _, candidate := range []string{"closed", "open", "half_open"} {
		value := 0.0
		if candidate == state {
			value = 1
		}
		r.paymentCircuitState.WithLabelValues(candidate).Set(value)
	}
}

func (r *Registry) RecordPaymentProviderCircuitTransition(state string) {
	r.paymentCircuitChanges.WithLabelValues(state).Inc()
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

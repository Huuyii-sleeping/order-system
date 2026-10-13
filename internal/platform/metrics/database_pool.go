package metrics

import (
	"database/sql"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

type databaseStatsProvider interface {
	Stats() sql.DBStats
}

// RegisterDatabasePool 把 database/sql 的实时池状态接入当前应用的独立注册表。
// Collector 在每次 /metrics 抓取时读取快照，不启动额外 goroutine。
func (r *Registry) RegisterDatabasePool(db *sql.DB) error {
	if r == nil || r.registry == nil {
		return errors.New("metrics registry is nil")
	}
	if db == nil {
		return errors.New("database pool is nil")
	}
	return r.registry.Register(newDatabasePoolCollector(db))
}

type databasePoolCollector struct {
	stats        databaseStatsProvider
	connections  *prometheus.Desc
	maxOpen      *prometheus.Desc
	waits        *prometheus.Desc
	waitDuration *prometheus.Desc
	closed       *prometheus.Desc
}

func newDatabasePoolCollector(stats databaseStatsProvider) *databasePoolCollector {
	return &databasePoolCollector{
		stats: stats,
		connections: prometheus.NewDesc(
			"order_system_database_pool_connections",
			"Current database connections by bounded state.",
			[]string{"state"}, nil,
		),
		maxOpen: prometheus.NewDesc(
			"order_system_database_pool_max_open_connections",
			"Configured maximum number of open database connections for this instance.",
			nil, nil,
		),
		waits: prometheus.NewDesc(
			"order_system_database_pool_waits_total",
			"Total waits for a database connection because the pool was exhausted.",
			nil, nil,
		),
		waitDuration: prometheus.NewDesc(
			"order_system_database_pool_wait_duration_seconds_total",
			"Total time spent waiting for a database connection because the pool was exhausted.",
			nil, nil,
		),
		closed: prometheus.NewDesc(
			"order_system_database_pool_closed_total",
			"Total database connections closed by the pool by bounded reason.",
			[]string{"reason"}, nil,
		),
	}
}

func (c *databasePoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connections
	ch <- c.maxOpen
	ch <- c.waits
	ch <- c.waitDuration
	ch <- c.closed
}

func (c *databasePoolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.stats.Stats()
	ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(stats.OpenConnections), "open")
	ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(stats.InUse), "in_use")
	ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(stats.Idle), "idle")
	ch <- prometheus.MustNewConstMetric(c.maxOpen, prometheus.GaugeValue, float64(stats.MaxOpenConnections))
	ch <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, stats.WaitDuration.Seconds())
	ch <- prometheus.MustNewConstMetric(c.closed, prometheus.CounterValue, float64(stats.MaxIdleClosed), "max_idle")
	ch <- prometheus.MustNewConstMetric(c.closed, prometheus.CounterValue, float64(stats.MaxIdleTimeClosed), "max_idle_time")
	ch <- prometheus.MustNewConstMetric(c.closed, prometheus.CounterValue, float64(stats.MaxLifetimeClosed), "max_lifetime")
}

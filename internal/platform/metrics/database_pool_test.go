package metrics

import (
	"database/sql"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type databaseStatsStub struct{ stats sql.DBStats }

func (s databaseStatsStub) Stats() sql.DBStats { return s.stats }

func TestDatabasePoolCollectorExportsStatsSnapshot(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(newDatabasePoolCollector(databaseStatsStub{stats: sql.DBStats{
		MaxOpenConnections: 20,
		OpenConnections:    18,
		InUse:              16,
		Idle:               2,
		WaitCount:          7,
		WaitDuration:       1500 * time.Millisecond,
		MaxIdleClosed:      3,
		MaxIdleTimeClosed:  4,
		MaxLifetimeClosed:  5,
	}}))

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather database pool metrics: %v", err)
	}
	got := make(map[string]float64)
	for _, family := range families {
		for _, metric := range family.Metric {
			key := family.GetName()
			for _, label := range metric.Label {
				key += "{" + label.GetName() + "=" + label.GetValue() + "}"
			}
			if metric.Gauge != nil {
				got[key] = metric.Gauge.GetValue()
			} else if metric.Counter != nil {
				got[key] = metric.Counter.GetValue()
			}
		}
	}
	want := map[string]float64{
		"order_system_database_pool_connections{state=open}":            18,
		"order_system_database_pool_connections{state=in_use}":          16,
		"order_system_database_pool_connections{state=idle}":            2,
		"order_system_database_pool_max_open_connections":               20,
		"order_system_database_pool_waits_total":                        7,
		"order_system_database_pool_wait_duration_seconds_total":        1.5,
		"order_system_database_pool_closed_total{reason=max_idle}":      3,
		"order_system_database_pool_closed_total{reason=max_idle_time}": 4,
		"order_system_database_pool_closed_total{reason=max_lifetime}":  5,
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("metric %s = %v, want %v", key, got[key], expected)
		}
	}
}

func TestRegisterDatabasePoolRejectsNil(t *testing.T) {
	if err := NewRegistry().RegisterDatabasePool(nil); err == nil {
		t.Fatal("RegisterDatabasePool(nil) error = nil")
	}
}

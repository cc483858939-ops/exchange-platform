package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	databasePoolRoleAPI    = "api"
	databasePoolRoleWorker = "worker"
)

var databasePoolCollector = newDatabasePoolCollector()

type databasePoolStatsCollector struct {
	mu    sync.RWMutex
	pools map[string]*sql.DB

	maxOpenConnections *prometheus.Desc
	openConnections    *prometheus.Desc
	inUseConnections   *prometheus.Desc
	idleConnections    *prometheus.Desc
	waitCount          *prometheus.Desc
	waitDuration       *prometheus.Desc
}

func newDatabasePoolCollector() *databasePoolStatsCollector {
	const namespace = "go_exchange_database_pool"
	return &databasePoolStatsCollector{
		pools:              make(map[string]*sql.DB, 2),
		maxOpenConnections: prometheus.NewDesc(namespace+"_max_open_connections", "Configured maximum number of open database connections.", []string{"role"}, nil),
		openConnections:    prometheus.NewDesc(namespace+"_open_connections", "Current number of open database connections.", []string{"role"}, nil),
		inUseConnections:   prometheus.NewDesc(namespace+"_in_use_connections", "Current number of in-use database connections.", []string{"role"}, nil),
		idleConnections:    prometheus.NewDesc(namespace+"_idle_connections", "Current number of idle database connections.", []string{"role"}, nil),
		waitCount:          prometheus.NewDesc(namespace+"_wait_count_total", "Cumulative number of waits for a database connection.", []string{"role"}, nil),
		waitDuration:       prometheus.NewDesc(namespace+"_wait_duration_seconds_total", "Cumulative time spent waiting for a database connection in seconds.", []string{"role"}, nil),
	}
}

// RegisterDatabasePool exposes scrape-time database/sql statistics for the
// API and Worker pools. Maintenance commands are short-lived and do not use
// the long-running /metrics endpoints.
func RegisterDatabasePool(role string, db *sql.DB) error {
	if role != databasePoolRoleAPI && role != databasePoolRoleWorker {
		return fmt.Errorf("unsupported database metrics role %q", role)
	}
	if db == nil {
		return errors.New("database metrics pool is nil")
	}
	return databasePoolCollector.register(role, db)
}

func (c *databasePoolStatsCollector) register(role string, db *sql.DB) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.pools[role]; existing != nil && existing != db {
		return fmt.Errorf("database metrics pool for role %q is already registered", role)
	}
	c.pools[role] = db
	return nil
}

func (c *databasePoolStatsCollector) unregister(role string, db *sql.DB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pools[role] == db {
		delete(c.pools, role)
	}
}

func (c *databasePoolStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.maxOpenConnections
	ch <- c.openConnections
	ch <- c.inUseConnections
	ch <- c.idleConnections
	ch <- c.waitCount
	ch <- c.waitDuration
}

func (c *databasePoolStatsCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	pools := make(map[string]*sql.DB, len(c.pools))
	for role, db := range c.pools {
		pools[role] = db
	}
	c.mu.RUnlock()

	for role, db := range pools {
		stats := db.Stats()
		labels := []string{role}
		ch <- prometheus.MustNewConstMetric(c.maxOpenConnections, prometheus.GaugeValue, float64(stats.MaxOpenConnections), labels...)
		ch <- prometheus.MustNewConstMetric(c.openConnections, prometheus.GaugeValue, float64(stats.OpenConnections), labels...)
		ch <- prometheus.MustNewConstMetric(c.inUseConnections, prometheus.GaugeValue, float64(stats.InUse), labels...)
		ch <- prometheus.MustNewConstMetric(c.idleConnections, prometheus.GaugeValue, float64(stats.Idle), labels...)
		ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stats.WaitCount), labels...)
		ch <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, stats.WaitDuration.Seconds(), labels...)
	}
}

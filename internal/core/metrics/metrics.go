package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is a lightweight in-memory request metrics collector. It tracks
// total request count, per-status counts, and average latency. Intended for
// a basic observability surface (GET /api/admin/metrics); for multi-instance
// deployments, switch to Prometheus or a similar external collector.
type Metrics struct {
	totalRequests    int64
	latencyCount     int64
	latencySumNanos  int64
	mu               sync.Mutex
	statusCounts     map[int]int64
}

// New creates an empty Metrics collector.
func New() *Metrics {
	return &Metrics{statusCounts: make(map[int]int64)}
}

// Record captures a single request's status code and latency.
func (m *Metrics) Record(status int, latency time.Duration) {
	atomic.AddInt64(&m.totalRequests, 1)
	atomic.AddInt64(&m.latencyCount, 1)
	atomic.AddInt64(&m.latencySumNanos, int64(latency))
	m.mu.Lock()
	m.statusCounts[status]++
	m.mu.Unlock()
}

// Snapshot is a point-in-time copy of collected metrics.
type Snapshot struct {
	TotalRequests int64            `json:"total_requests"`
	StatusCounts  map[int]int64    `json:"status_counts"`
	AvgLatencyMs  float64          `json:"avg_latency_ms"`
}

// Snapshot returns a copy-safe view of current metrics.
func (m *Metrics) Snapshot() Snapshot {
	count := atomic.LoadInt64(&m.latencyCount)
	sum := atomic.LoadInt64(&m.latencySumNanos)
	var avg float64
	if count > 0 {
		avg = float64(sum) / float64(count) / float64(time.Millisecond)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	statusCopy := make(map[int]int64, len(m.statusCounts))
	for k, v := range m.statusCounts {
		statusCopy[k] = v
	}
	return Snapshot{
		TotalRequests: atomic.LoadInt64(&m.totalRequests),
		StatusCounts:  statusCopy,
		AvgLatencyMs:  avg,
	}
}

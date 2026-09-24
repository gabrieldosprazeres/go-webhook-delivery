// Package telemetry exposes bounded Prometheus metrics and safe OpenTelemetry traces.
package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a process-local registry so tests and binaries never share mutable globals.
type Metrics struct {
	registry         *prometheus.Registry
	httpRequests     *prometheus.CounterVec
	httpDuration     *prometheus.HistogramVec
	ingestDuration   prometheus.Histogram
	attempts         *prometheus.CounterVec
	deliveryDuration *prometheus.HistogramVec
	retries          *prometheus.CounterVec
	deadLetters      *prometheus.CounterVec
	fencingRejected  prometheus.Counter
	queueReady       prometheus.Gauge
	queueOldest      prometheus.Gauge
	queueSampleOK    prometheus.Gauge
	activeWorkers    prometheus.Gauge
	inflight         prometheus.Gauge
	purgeItems       *prometheus.CounterVec
	purgeLag         prometheus.Gauge
}

func NewMetrics() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wde_http_requests_total", Help: "HTTP requests by bounded route, method and status class."}, []string{"route", "method", "status_class"})
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wde_http_request_duration_seconds", Help: "HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"route", "method"})
	m.ingestDuration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "wde_event_ingest_duration_seconds", Help: "Event ingestion duration.", Buckets: prometheus.DefBuckets})
	m.attempts = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wde_delivery_attempts_total", Help: "Finalized delivery attempts."}, []string{"outcome", "category"})
	m.deliveryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wde_delivery_duration_seconds", Help: "Delivery attempt duration.", Buckets: prometheus.DefBuckets}, []string{"outcome"})
	m.retries = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wde_delivery_retries_total", Help: "Scheduled delivery retries."}, []string{"category"})
	m.deadLetters = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wde_delivery_dead_letter_total", Help: "Deliveries moved to dead letter."}, []string{"category"})
	m.fencingRejected = prometheus.NewCounter(prometheus.CounterOpts{Name: "wde_fencing_rejections_total", Help: "Finalizations rejected by lease or fencing."})
	m.queueReady = prometheus.NewGauge(prometheus.GaugeOpts{Name: "wde_queue_ready", Help: "Deliveries currently eligible for claim."})
	m.queueOldest = prometheus.NewGauge(prometheus.GaugeOpts{Name: "wde_queue_oldest_seconds", Help: "Age of the oldest eligible delivery."})
	m.queueSampleOK = prometheus.NewGauge(prometheus.GaugeOpts{Name: "wde_queue_sample_success", Help: "Whether the last queue sample succeeded."})
	m.activeWorkers = prometheus.NewGauge(prometheus.GaugeOpts{Name: "wde_active_workers", Help: "Worker schedulers accepting claims."})
	m.inflight = prometheus.NewGauge(prometheus.GaugeOpts{Name: "wde_inflight_deliveries", Help: "Delivery jobs currently in flight."})
	m.purgeItems = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wde_purge_items_total", Help: "Purged items by bounded category."}, []string{"category"})
	m.purgeLag = prometheus.NewGauge(prometheus.GaugeOpts{Name: "wde_purge_lag_seconds", Help: "Age of the oldest actionable retention item."})
	m.registry.MustRegister(m.httpRequests, m.httpDuration, m.ingestDuration, m.attempts,
		m.deliveryDuration, m.retries, m.deadLetters, m.fencingRejected, m.queueReady,
		m.queueOldest, m.queueSampleOK, m.activeWorkers, m.inflight, m.purgeItems, m.purgeLag)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (m *Metrics) observeHTTP(route, method string, status int, duration time.Duration) {
	method = boundedMethod(method)
	m.httpRequests.WithLabelValues(route, method, statusClass(status)).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(duration.Seconds())
	if route == "/v1/events" {
		m.ingestDuration.Observe(duration.Seconds())
	}
}

func boundedMethod(method string) string {
	if method == http.MethodGet || method == http.MethodPost {
		return method
	}
	return "OTHER"
}

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "other"
	}
	return string(rune('0'+status/100)) + "xx"
}

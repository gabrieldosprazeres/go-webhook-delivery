package telemetry

import "time"

var allowedCategories = map[string]struct{}{
	"none": {}, "cancelled": {}, "timeout": {}, "network": {}, "redirect": {},
	"ssrf_policy": {}, "response_read": {}, "response_too_large": {},
	"invalid_http_status": {}, "http_retryable": {}, "http_permanent": {},
	"invalid_target": {}, "request_build": {}, "http_destination_disabled": {},
	"payload_expired": {}, "restore_quarantine": {},
}

var allowedPurgeCategories = map[string]struct{}{
	"payload": {}, "secret": {}, "attempt": {}, "replay": {}, "rotation": {},
	"delivery": {}, "event": {}, "audit": {}, "bucket": {}, "workspace": {},
}

func boundedCategory(category string) string {
	if category == "" {
		return "none"
	}
	if _, ok := allowedCategories[category]; ok {
		return category
	}
	return "other"
}

// ObserveAttempt records only bounded outcome/category dimensions.
func (m *Metrics) ObserveAttempt(outcome, category string, duration time.Duration, retry, deadLetter, changed bool) {
	if outcome != "success" && outcome != "retry" && outcome != "permanent_failure" {
		outcome = "other"
	}
	category = boundedCategory(category)
	if !changed {
		m.fencingRejected.Inc()
		return
	}
	m.attempts.WithLabelValues(outcome, category).Inc()
	m.deliveryDuration.WithLabelValues(outcome).Observe(max(duration.Seconds(), 0))
	if retry {
		m.retries.WithLabelValues(category).Inc()
	}
	if deadLetter {
		m.deadLetters.WithLabelValues(category).Inc()
	}
}

func (m *Metrics) SetInflight(value int) { m.inflight.Set(float64(max(value, 0))) }
func (m *Metrics) SetWorkerActive(active bool) {
	if active {
		m.activeWorkers.Set(1)
	} else {
		m.activeWorkers.Set(0)
	}
}
func (m *Metrics) SetQueue(ready int64, oldest time.Duration) {
	m.queueReady.Set(float64(max(ready, 0)))
	m.queueOldest.Set(max(oldest.Seconds(), 0))
	m.queueSampleOK.Set(1)
}

func (m *Metrics) QueueSampleFailed() { m.queueSampleOK.Set(0) }

func (m *Metrics) ObservePurge(category string, count int64) {
	if count > 0 {
		if _, ok := allowedPurgeCategories[category]; !ok {
			category = "other"
		}
		m.purgeItems.WithLabelValues(category).Add(float64(count))
	}
}

func (m *Metrics) SetPurgeLag(age time.Duration) { m.purgeLag.Set(max(age.Seconds(), 0)) }

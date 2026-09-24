package main

import (
	"encoding/json"
	"os"
	"runtime"
	"sort"
	"time"
)

type sample struct {
	Goroutines int    `json:"goroutines"`
	HeapBytes  uint64 `json:"heap_bytes"`
}

type report struct {
	Round       int `json:"round"`
	Environment struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
		CPU  int    `json:"logical_cpu"`
		Go   string `json:"go_version"`
	} `json:"environment"`
	Dataset struct {
		Events      int `json:"events"`
		Concurrency int `json:"concurrency"`
	} `json:"dataset"`
	Ingestion struct {
		RequestsPerSecond float64 `json:"requests_per_second"`
		P95Milliseconds   float64 `json:"p95_milliseconds"`
		DurationSeconds   float64 `json:"duration_seconds"`
	} `json:"ingestion"`
	Delivery struct {
		RequestsPerSecond float64 `json:"requests_per_second"`
		DurationSeconds   float64 `json:"duration_seconds"`
		Unique            int     `json:"unique"`
		Duplicates        int     `json:"duplicates"`
		Missing           int     `json:"missing"`
		Unexpected        int     `json:"unexpected"`
		InvalidSignatures int     `json:"invalid_signatures"`
	} `json:"delivery"`
	Queue   queueStatus `json:"queue"`
	Process struct {
		Before        sample         `json:"before"`
		During        []processPoint `json:"during"`
		AfterTeardown []processPoint `json:"after_teardown_gc"`
		Thresholds    struct {
			MaximumGoroutineGrowth int    `json:"maximum_goroutine_growth"`
			MaximumHeapGrowthBytes uint64 `json:"maximum_heap_growth_bytes"`
		} `json:"thresholds"`
		Passed bool `json:"passed"`
	} `json:"process"`
	System struct {
		Samples    []systemPoint `json:"samples"`
		Thresholds struct {
			MaximumRSSGrowthKiB       int64 `json:"maximum_rss_growth_kib"`
			MaximumMonotonicGrowthKiB int64 `json:"maximum_monotonic_growth_kib"`
		} `json:"thresholds"`
		Passed bool `json:"passed"`
	} `json:"system"`
}

func processSample() sample {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return sample{Goroutines: runtime.NumGoroutine(), HeapBytes: memory.HeapAlloc}
}

func buildReport(opts options, load loadResult, delivery deliveryResult, queue queueStatus, evidence resourceEvidence) report {
	var result report
	result.Round = opts.round
	result.Environment.OS, result.Environment.Arch = runtime.GOOS, runtime.GOARCH
	result.Environment.CPU, result.Environment.Go = runtime.NumCPU(), runtime.Version()
	result.Dataset.Events, result.Dataset.Concurrency = opts.events, opts.concurrency
	result.Ingestion.DurationSeconds = load.duration.Seconds()
	result.Ingestion.RequestsPerSecond = perSecond(opts.events, load.duration)
	latencies := append([]time.Duration(nil), load.latencies...)
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	result.Ingestion.P95Milliseconds = float64(latencies[(len(latencies)*95-1)/100]) / float64(time.Millisecond)
	result.Delivery.DurationSeconds = delivery.duration.Seconds()
	result.Delivery.RequestsPerSecond = perSecond(delivery.unique-1, delivery.duration)
	result.Delivery.Unique = delivery.unique
	result.Delivery.Duplicates = delivery.duplicates
	result.Delivery.Missing = delivery.missing
	result.Delivery.Unexpected = delivery.unexpected
	result.Delivery.InvalidSignatures = delivery.invalidSignatures
	result.Queue = queue
	result.Process.Before = evidence.before
	result.Process.During = evidence.during
	result.Process.AfterTeardown = evidence.afterTeardown
	result.Process.Thresholds.MaximumGoroutineGrowth = 6
	result.Process.Thresholds.MaximumHeapGrowthBytes = 16 << 20
	result.Process.Passed = true
	result.System.Samples = evidence.system
	result.System.Thresholds.MaximumRSSGrowthKiB = 128 << 10
	result.System.Thresholds.MaximumMonotonicGrowthKiB = 32 << 10
	result.System.Passed = true
	return result
}

func perSecond(count int, duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(count) / duration.Seconds()
}

func writeReport(path string, result report) error {
	return writeJSON(path, result)
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if path == "" {
		_, err = os.Stdout.Write(encoded)
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

package main

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
)

type statistics struct {
	Minimum           float64 `json:"minimum"`
	Maximum           float64 `json:"maximum"`
	Median            float64 `json:"median"`
	Mean              float64 `json:"mean"`
	StandardDeviation float64 `json:"standard_deviation"`
	P95               float64 `json:"p95"`
}

type suiteReport struct {
	Mode           string   `json:"mode"`
	WarmupExcluded bool     `json:"warmup_excluded"`
	Rounds         []report `json:"rounds"`
	Summary        struct {
		IngestionRPS statistics `json:"ingestion_requests_per_second"`
		IngestionP95 statistics `json:"ingestion_p95_milliseconds"`
		DeliveryRPS  statistics `json:"delivery_requests_per_second"`
	} `json:"summary"`
}

func aggregateReports(output, directory, mode string) error {
	if mode != "benchmark" && mode != "soak" {
		return errors.New("invalid aggregate mode")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var suite suiteReport
	suite.Mode, suite.WarmupExcluded = mode, true
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			return readErr
		}
		var current report
		if json.Unmarshal(raw, &current) != nil || !completeRound(current) {
			return errors.New("invalid or incomplete round report")
		}
		suite.Rounds = append(suite.Rounds, current)
	}
	if len(suite.Rounds) == 0 || mode == "benchmark" && len(suite.Rounds) < 3 {
		return errors.New("insufficient measured rounds")
	}
	sort.Slice(suite.Rounds, func(i, j int) bool { return suite.Rounds[i].Round < suite.Rounds[j].Round })
	var ingest, ingestP95, delivery []float64
	for _, current := range suite.Rounds {
		ingest = append(ingest, current.Ingestion.RequestsPerSecond)
		ingestP95 = append(ingestP95, current.Ingestion.P95Milliseconds)
		delivery = append(delivery, current.Delivery.RequestsPerSecond)
	}
	suite.Summary.IngestionRPS = describe(ingest)
	suite.Summary.IngestionP95 = describe(ingestP95)
	suite.Summary.DeliveryRPS = describe(delivery)
	return writeJSON(output, suite)
}

func completeRound(current report) bool {
	events := current.Dataset.Events
	return current.Round >= 0 && events >= 2 && current.Dataset.Concurrency > 0 &&
		finitePositive(current.Ingestion.RequestsPerSecond) &&
		finitePositive(current.Ingestion.P95Milliseconds) &&
		finitePositive(current.Delivery.RequestsPerSecond) &&
		current.Delivery.Unique == events && current.Delivery.Missing == 0 &&
		current.Delivery.Unexpected == 0 && current.Delivery.InvalidSignatures == 0 &&
		current.Queue.Backlog == 0 &&
		current.Queue.Succeeded == events && current.Queue.Failed == 0 &&
		current.Queue.Total == events && current.Process.Passed && current.System.Passed
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func describe(values []float64) statistics {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var result statistics
	result.Minimum, result.Maximum = sorted[0], sorted[len(sorted)-1]
	result.Median, result.P95 = percentile(sorted, 0.50), percentile(sorted, 0.95)
	for _, value := range sorted {
		result.Mean += value
	}
	result.Mean /= float64(len(sorted))
	for _, value := range sorted {
		result.StandardDeviation += math.Pow(value-result.Mean, 2)
	}
	result.StandardDeviation = math.Sqrt(result.StandardDeviation / float64(len(sorted)))
	return result
}

func percentile(sorted []float64, quantile float64) float64 {
	index := int(math.Ceil(float64(len(sorted))*quantile)) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

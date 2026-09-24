package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAggregateRequiresThreeCompleteBenchmarkRounds(t *testing.T) {
	directory := t.TempDir()
	for round, rate := range []float64{100, 300, 200} {
		var current report
		current.Round = round + 1
		current.Dataset.Events = 2
		current.Dataset.Concurrency = 1
		current.Ingestion.RequestsPerSecond = rate
		current.Ingestion.P95Milliseconds = rate / 10
		current.Delivery.RequestsPerSecond = rate / 2
		current.Delivery.Unique = 2
		current.Queue.Succeeded = 2
		current.Queue.Total = 2
		current.Process.Passed = true
		current.System.Passed = true
		if err := writeReport(filepath.Join(directory, "round-"+string(rune('1'+round))+".json"), current); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "summary.json")
	if err := aggregateReports(output, directory, "benchmark"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var summary suiteReport
	if json.Unmarshal(raw, &summary) != nil || len(summary.Rounds) != 3 ||
		summary.Summary.IngestionRPS.Median != 200 || summary.Summary.IngestionRPS.Minimum != 100 ||
		summary.Summary.IngestionRPS.Maximum != 300 {
		t.Fatalf("unexpected aggregate: %+v", summary)
	}
}

func TestAggregateRejectsRoundWhoseDuplicateMasksMissingOrQueue(t *testing.T) {
	directory := t.TempDir()
	var current report
	current.Round = 1
	current.Dataset.Events, current.Dataset.Concurrency = 2, 1
	current.Ingestion.RequestsPerSecond, current.Ingestion.P95Milliseconds = 100, 10
	current.Delivery.RequestsPerSecond = 50
	current.Delivery.Unique, current.Delivery.Duplicates, current.Delivery.Missing = 1, 1, 1
	current.Queue.Backlog, current.Queue.Succeeded, current.Queue.Total = 1, 1, 2
	current.Process.Passed, current.System.Passed = true, true
	if err := writeReport(filepath.Join(directory, "round-1.json"), current); err != nil {
		t.Fatal(err)
	}
	if err := aggregateReports(filepath.Join(t.TempDir(), "summary.json"), directory, "soak"); err == nil {
		t.Fatal("incomplete unique delivery set accepted")
	}
}

func TestSystemEvidenceFailsClosedForBacklogAndAnomalousGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.tsv")
	valid := "0|100|100|2|0\n250|110|110|0|2\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if points, err := readSystemSamples(path, 2); err != nil || len(points) != 2 {
		t.Fatalf("valid evidence rejected: points=%d err=%v", len(points), err)
	}
	if err := os.WriteFile(path, []byte("0|100|100|1|1\n250|110|110|1|1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSystemSamples(path, 2); err == nil {
		t.Fatal("actionable backlog accepted")
	}
	monotonic := "0|100|100|1|1\n250|10000|10000|1|1\n500|20000|20000|1|1\n" +
		"750|30000|30000|1|1\n1000|40000|40000|0|2\n"
	if err := os.WriteFile(path, []byte(monotonic), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSystemSamples(path, 2); err == nil {
		t.Fatal("monotonic RSS growth above threshold accepted")
	}
	after := []processPoint{
		{Sample: sample{Goroutines: 1, HeapBytes: 1}},
		{Sample: sample{Goroutines: 2, HeapBytes: 300_000}},
		{Sample: sample{Goroutines: 3, HeapBytes: 600_000}},
		{Sample: sample{Goroutines: 4, HeapBytes: 900_000}},
		{Sample: sample{Goroutines: 5, HeapBytes: 1_200_000}},
	}
	if err := validateProcessGrowth(sample{}, after); err == nil {
		t.Fatal("monotonic post-GC growth accepted")
	}
}

package main

import (
	"bufio"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type processPoint struct {
	ElapsedMilliseconds int64  `json:"elapsed_milliseconds"`
	Phase               string `json:"phase"`
	Sample              sample `json:"sample"`
}

type systemPoint struct {
	ElapsedMilliseconds int64 `json:"elapsed_milliseconds"`
	APIRSSKiB           int64 `json:"api_rss_kib"`
	WorkerRSSKiB        int64 `json:"worker_rss_kib"`
	Backlog             int   `json:"backlog"`
	Succeeded           int   `json:"succeeded"`
}

type processSampler struct {
	mu      sync.Mutex
	once    sync.Once
	started time.Time
	points  []processPoint
	stop    chan struct{}
	done    chan struct{}
}

func startProcessSampler() *processSampler {
	sampler := &processSampler{started: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	sampler.add("running")
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sampler.add("running")
			case <-sampler.stop:
				return
			}
		}
	}()
	return sampler
}

func (s *processSampler) add(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.points = append(s.points, processPoint{ElapsedMilliseconds: time.Since(s.started).Milliseconds(),
		Phase: phase, Sample: processSample()})
}

func (s *processSampler) finish() []processPoint {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		s.add("running_final")
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]processPoint(nil), s.points...)
}

func postTeardownSamples(started time.Time) []processPoint {
	points := make([]processPoint, 0, 5)
	for index := 0; index < 5; index++ {
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		points = append(points, processPoint{ElapsedMilliseconds: time.Since(started).Milliseconds(),
			Phase: "after_teardown_gc", Sample: processSample()})
	}
	return points
}

func validateProcessGrowth(before sample, after []processPoint) error {
	if len(after) < 5 {
		return errors.New("insufficient post-teardown samples")
	}
	last := after[len(after)-1].Sample
	if last.Goroutines > before.Goroutines+6 || last.HeapBytes > before.HeapBytes+(16<<20) {
		return errors.New("post-teardown process growth exceeded threshold")
	}
	if monotonicProcess(after) {
		return errors.New("post-teardown process growth remained monotonic")
	}
	return nil
}

func monotonicProcess(points []processPoint) bool {
	heapIncreasing, goroutineIncreasing := true, true
	for index := 1; index < len(points); index++ {
		heapIncreasing = heapIncreasing && points[index].Sample.HeapBytes > points[index-1].Sample.HeapBytes
		goroutineIncreasing = goroutineIncreasing && points[index].Sample.Goroutines > points[index-1].Sample.Goroutines
	}
	var heapDelta uint64
	if points[len(points)-1].Sample.HeapBytes > points[0].Sample.HeapBytes {
		heapDelta = points[len(points)-1].Sample.HeapBytes - points[0].Sample.HeapBytes
	}
	goroutineDelta := points[len(points)-1].Sample.Goroutines - points[0].Sample.Goroutines
	return heapIncreasing && heapDelta > 1<<20 || goroutineIncreasing && goroutineDelta > 2
}

type resourceEvidence struct {
	before        sample
	during        []processPoint
	afterTeardown []processPoint
	system        []systemPoint
}

func readSystemSamples(path string, expected int) ([]systemPoint, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var points []systemPoint
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "|")
		if len(parts) != 5 {
			return nil, errors.New("invalid system sample")
		}
		values := make([]int64, len(parts))
		for index, part := range parts {
			values[index], err = strconv.ParseInt(part, 10, 64)
			if err != nil || values[index] < 0 {
				return nil, errors.New("invalid system sample value")
			}
		}
		points = append(points, systemPoint{ElapsedMilliseconds: values[0], APIRSSKiB: values[1],
			WorkerRSSKiB: values[2], Backlog: int(values[3]), Succeeded: int(values[4])})
	}
	if scanner.Err() != nil || len(points) < 2 {
		return nil, errors.New("insufficient system samples")
	}
	last := points[len(points)-1]
	if last.Backlog != 0 || last.Succeeded != expected {
		return nil, errors.New("final system sample has actionable backlog")
	}
	if last.APIRSSKiB-points[0].APIRSSKiB > 128<<10 || last.WorkerRSSKiB-points[0].WorkerRSSKiB > 128<<10 {
		return nil, errors.New("RSS growth exceeded threshold")
	}
	if monotonicRSS(points) {
		return nil, errors.New("RSS growth remained monotonic above threshold")
	}
	return points, nil
}

func monotonicRSS(points []systemPoint) bool {
	if len(points) < 5 {
		return false
	}
	points = points[len(points)-5:]
	apiIncreasing, workerIncreasing := true, true
	for index := 1; index < len(points); index++ {
		apiIncreasing = apiIncreasing && points[index].APIRSSKiB > points[index-1].APIRSSKiB
		workerIncreasing = workerIncreasing && points[index].WorkerRSSKiB > points[index-1].WorkerRSSKiB
	}
	return apiIncreasing && points[len(points)-1].APIRSSKiB-points[0].APIRSSKiB > 32<<10 ||
		workerIncreasing && points[len(points)-1].WorkerRSSKiB-points[0].WorkerRSSKiB > 32<<10
}

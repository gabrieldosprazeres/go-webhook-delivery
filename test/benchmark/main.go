// Command benchmark drives the real API and worker against a loopback receiver.
// It is test-only tooling and is never included in production images.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
)

type options struct {
	apiURL, credential, listen, target, output, ingestedSignal string
	deliveredSignal, databaseStatus, systemSamples             string
	cpuProfile, heapProfile                                    string
	events, concurrency                                        int
	round                                                      int
	timeout                                                    time.Duration
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "aggregate" {
		if len(os.Args) != 5 {
			_, _ = fmt.Fprintln(os.Stderr, "benchmark: aggregate OUTPUT ROUNDS_DIRECTORY MODE")
			os.Exit(2)
		}
		if err := aggregateReports(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "benchmark:", err)
			os.Exit(1)
		}
		return
	}
	var opts options
	flag.StringVar(&opts.apiURL, "api", "http://127.0.0.1:18080", "loopback API URL")
	flag.StringVar(&opts.credential, "credential", "", "0600 bootstrap credential JSON")
	flag.StringVar(&opts.listen, "listen", "127.0.0.1:18082", "loopback receiver address")
	flag.StringVar(&opts.target, "target", "http://127.0.0.1:18082/webhook", "loopback endpoint URL")
	flag.StringVar(&opts.output, "output", "", "optional JSON result path")
	flag.StringVar(&opts.ingestedSignal, "ingested-signal", "", "optional private phase signal path")
	flag.StringVar(&opts.deliveredSignal, "delivered-signal", "", "private delivery-complete signal path")
	flag.StringVar(&opts.databaseStatus, "database-status", "", "private queue status JSON path")
	flag.StringVar(&opts.systemSamples, "system-samples", "", "private RSS/backlog samples path")
	flag.StringVar(&opts.cpuProfile, "cpu-profile", "", "optional local CPU profile")
	flag.StringVar(&opts.heapProfile, "heap-profile", "", "optional local heap profile")
	flag.IntVar(&opts.events, "events", 1000, "events to ingest")
	flag.IntVar(&opts.concurrency, "concurrency", 32, "ingestion concurrency")
	flag.IntVar(&opts.round, "round", 1, "one-based measured round; zero means warmup")
	flag.DurationVar(&opts.timeout, "timeout", 90*time.Second, "whole run timeout")
	flag.Parse()
	if err := run(opts); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "benchmark:", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	if err := validateOptions(opts); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	stopProfile, err := startCPUProfile(opts.cpuProfile)
	if err != nil {
		return err
	}
	var profileOnce sync.Once
	stopProfileOnce := func() { profileOnce.Do(stopProfile) }
	defer stopProfileOnce()
	before := processSample()
	processSampler := startProcessSampler()
	defer processSampler.finish()
	receiver := newReceiver()
	server := &http.Server{Addr: opts.listen, Handler: receiver, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	listener, err := loopbackListen(opts.listen)
	if err != nil {
		return err
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	client, err := newBenchClient(opts.apiURL, opts.credential)
	if err != nil {
		return err
	}
	if err = client.createEndpoint(ctx, opts.target, receiver); err != nil {
		return err
	}
	defer receiver.clearSecret()
	load, err := client.ingest(ctx, opts.events, opts.concurrency)
	if err != nil {
		return err
	}
	if err = receiver.expect(load.deliveryIDs); err != nil {
		return err
	}
	if err = writeSignal(opts.ingestedSignal); err != nil {
		return err
	}
	if err = receiver.waitAll(ctx); err != nil {
		return err
	}
	if err = writeSignal(opts.deliveredSignal); err != nil {
		return err
	}
	queue, err := waitQueueStatus(ctx, opts.databaseStatus, opts.events)
	if err != nil {
		return err
	}
	system, err := readSystemSamples(opts.systemSamples, opts.events)
	if err != nil {
		return err
	}
	shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
	if err = server.Shutdown(shutdown); err != nil {
		done()
		return err
	}
	done()
	receiver.quiesce()
	delivery, err := receiver.snapshot()
	if err != nil {
		return err
	}
	during := processSampler.finish()
	stopProfileOnce()
	client.http.CloseIdleConnections()
	receiver.clearSecret()
	after := postTeardownSamples(processSampler.started)
	if err = validateProcessGrowth(before, after); err != nil {
		return err
	}
	runtime.GC()
	evidence := resourceEvidence{before: before, during: during, afterTeardown: after, system: system}
	report := buildReport(opts, load, delivery, queue, evidence)
	if err = writeReport(opts.output, report); err != nil {
		return err
	}
	return writeHeapProfile(opts.heapProfile)
}

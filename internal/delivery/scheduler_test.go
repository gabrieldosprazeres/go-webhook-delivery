package delivery

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type schedulerStore struct {
	mu              sync.Mutex
	remaining       int
	maxClaimRequest int
	started         chan struct{}
	release         chan struct{}
	ignoreContext   bool
	panicFinalize   bool
	blockClaim      bool
	claimDeadline   time.Time
}

func (s *schedulerStore) ClaimBatch(ctx context.Context, request ClaimRequest) ([]Claim, error) {
	if s.blockClaim {
		if s.started != nil {
			s.started <- struct{}{}
		}
		select {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimDeadline, _ = ctx.Deadline()
	if request.Limit > s.maxClaimRequest {
		s.maxClaimRequest = request.Limit
	}
	count := min(request.Limit, s.remaining)
	s.remaining -= count
	claims := make([]Claim, count)
	for index := range claims {
		claims[index] = Claim{DeliveryID: uuid.New(), AttemptNumber: 1, MaxAttempts: 10}
	}
	return claims, nil
}

func TestClaimUsesConfiguredDatabaseDeadline(t *testing.T) {
	store := &schedulerStore{}
	runner := testScheduler(store, RunnerOptions{ClaimTimeout: 40 * time.Millisecond})
	started := time.Now()
	if _, err := runner.claim(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	deadline := store.claimDeadline
	store.mu.Unlock()
	remaining := time.Until(deadline)
	if deadline.IsZero() || remaining <= 0 || deadline.Sub(started) > 50*time.Millisecond {
		t.Fatalf("claim deadline=%s elapsed budget=%s", deadline, deadline.Sub(started))
	}
}

func (s *schedulerStore) Finalize(ctx context.Context, _ Claim, _ uuid.UUID, _ Result) (bool, error) {
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.panicFinalize {
		panic("hostile finalize")
	}
	if s.ignoreContext {
		select {}
	}
	if s.release == nil {
		return true, nil
	}
	select {
	case <-s.release:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func TestSchedulerHardDeadlineWithNonCooperativeJob(t *testing.T) {
	store := &schedulerStore{remaining: 1, started: make(chan struct{}, 1), ignoreContext: true}
	runner := testScheduler(store, RunnerOptions{Concurrency: 1, BatchSize: 1, Poll: 10 * time.Millisecond, Shutdown: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-store.started
	started := time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("hard shutdown deadline exceeded: %s", elapsed)
	}
}

func TestSchedulerReturnsClaimAndJobPanicsAsErrors(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		store := &panicClaimStore{}
		runner := testScheduler(store, RunnerOptions{Poll: 10 * time.Millisecond, Shutdown: 100 * time.Millisecond})
		if err := runner.Run(context.Background()); !errors.Is(err, ErrJobPanic) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("job", func(t *testing.T) {
		store := &schedulerStore{remaining: 1, panicFinalize: true}
		runner := testScheduler(store, RunnerOptions{Concurrency: 1, BatchSize: 1, Poll: 10 * time.Millisecond, Shutdown: 100 * time.Millisecond})
		if err := runner.Run(context.Background()); !errors.Is(err, ErrJobPanic) {
			t.Fatalf("error=%v", err)
		}
	})
}

type panicClaimStore struct{ schedulerStore }

func (*panicClaimStore) ClaimBatch(context.Context, ClaimRequest) ([]Claim, error) {
	panic("hostile claim")
}

type recoveringSchedulerStore struct {
	schedulerStore
	mu             sync.Mutex
	claimFailures  int
	finalizeErrors int
	claimCalls     int
	finalizeCalls  int
	completed      chan struct{}
}

func (s *recoveringSchedulerStore) ClaimBatch(ctx context.Context, request ClaimRequest) ([]Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls++
	if s.claimCalls <= s.claimFailures {
		return nil, errors.New("transient claim failure")
	}
	if s.finalizeCalls <= s.finalizeErrors {
		return []Claim{{DeliveryID: uuid.New(), AttemptNumber: 1, MaxAttempts: 10}}, nil
	}
	return nil, nil
}

func (s *recoveringSchedulerStore) Finalize(context.Context, Claim, uuid.UUID, Result) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCalls++
	if s.finalizeCalls <= s.finalizeErrors {
		return false, errors.New("transient finalize failure")
	}
	if s.completed != nil {
		select {
		case <-s.completed:
		default:
			close(s.completed)
		}
	}
	return true, nil
}

func TestSchedulerRecoversFromTransientStoreFailures(t *testing.T) {
	store := &recoveringSchedulerStore{
		claimFailures: 2, finalizeErrors: 1, completed: make(chan struct{}),
	}
	runner := testScheduler(store, RunnerOptions{
		Concurrency: 1, BatchSize: 1, Poll: time.Millisecond,
		PollJitter: func(time.Duration) time.Duration { return 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-store.completed:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("scheduler did not recover from transient store failures")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.claimCalls < 4 || store.finalizeCalls != 2 {
		t.Fatalf("claim calls=%d finalize calls=%d", store.claimCalls, store.finalizeCalls)
	}
}

func TestSchedulerCancellationEscapesBlockedClaim(t *testing.T) {
	store := &schedulerStore{blockClaim: true, started: make(chan struct{}, 1)}
	runner := testScheduler(store, RunnerOptions{Poll: 10 * time.Millisecond, Shutdown: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-store.started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("blocked claim prevented shutdown")
	}
}

func TestSchedulerSIGTERMWithActiveNonCooperativeJob(t *testing.T) {
	if os.Getenv("WDE_SCHEDULER_SIGNAL_HELPER") == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		store := &schedulerStore{remaining: 1, ignoreContext: true, started: make(chan struct{}, 1)}
		runner := testScheduler(store, RunnerOptions{Concurrency: 1, BatchSize: 1, Poll: 10 * time.Millisecond, Shutdown: 100 * time.Millisecond})
		go func() {
			<-store.started
			fmt.Println("job-started")
		}()
		if err := runner.Run(ctx); err != nil {
			os.Exit(2)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestSchedulerSIGTERMWithActiveNonCooperativeJob$")
	command.Env = append(os.Environ(), "WDE_SCHEDULER_SIGNAL_HELPER=1", "GORACE=atexit_sleep_ms=0")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	ready := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "job-started") {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatalf("helper exited before active job: %s", stderr.String())
		}
	case <-time.After(2 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("active job did not start")
	}
	started := time.Now()
	if err = command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("helper exit: %v stderr=%s", err, stderr.String())
		}
	case <-time.After(500 * time.Millisecond):
		_ = command.Process.Kill()
		t.Fatal("SIGTERM exceeded hard deadline")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("SIGTERM shutdown elapsed=%s", elapsed)
	}
}

type boundedLoadStore struct {
	mu        sync.Mutex
	remaining int
	processed int
	total     int
	done      chan struct{}
}

func (s *boundedLoadStore) ClaimBatch(_ context.Context, request ClaimRequest) ([]Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := min(request.Limit, s.remaining)
	s.remaining -= count
	claims := make([]Claim, count)
	for index := range claims {
		claims[index] = Claim{DeliveryID: uuid.New(), AttemptNumber: 1, MaxAttempts: 10}
	}
	return claims, nil
}

func (s *boundedLoadStore) Finalize(context.Context, Claim, uuid.UUID, Result) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed++
	if s.processed == s.total {
		close(s.done)
	}
	return true, nil
}

func (*boundedLoadStore) Get(context.Context, uuid.UUID, uuid.UUID) (Details, error) {
	return Details{}, ErrNotFound
}

func TestSchedulerProlongedLoadKeepsResourcesBounded(t *testing.T) {
	const total = 10_000
	runtime.GC()
	beforeGoroutines := runtime.NumGoroutine()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	store := &boundedLoadStore{remaining: total, total: total, done: make(chan struct{})}
	runner := testScheduler(store, RunnerOptions{Concurrency: 16, BatchSize: 16, Poll: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()
	select {
	case <-store.done:
		cancel()
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("prolonged load did not complete")
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if got := runtime.NumGoroutine(); got > beforeGoroutines+8 {
		t.Fatalf("goroutines before=%d after=%d", beforeGoroutines, got)
	}
	if after.Alloc > before.Alloc+16<<20 {
		t.Fatalf("allocated memory grew from %d to %d", before.Alloc, after.Alloc)
	}
}

func (*schedulerStore) Get(context.Context, uuid.UUID, uuid.UUID) (Details, error) {
	return Details{}, ErrNotFound
}

func TestSchedulerNeverClaimsBeyondFreeCapacity(t *testing.T) {
	store := &schedulerStore{remaining: 20}
	runner := testScheduler(store, RunnerOptions{Concurrency: 3, BatchSize: 8, Poll: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.maxClaimRequest > 3 {
		t.Fatalf("claim limit=%d exceeds capacity=3", store.maxClaimRequest)
	}
}

func TestSchedulerDrainsBeforeShutdown(t *testing.T) {
	store := &schedulerStore{remaining: 1, started: make(chan struct{}, 1), release: make(chan struct{})}
	runner := testScheduler(store, RunnerOptions{Concurrency: 1, BatchSize: 1, Poll: 10 * time.Millisecond, Shutdown: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-store.started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("runner returned before drain: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(store.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerCancelsJobsAtGraceDeadline(t *testing.T) {
	store := &schedulerStore{remaining: 1, started: make(chan struct{}, 1), release: make(chan struct{})}
	runner := testScheduler(store, RunnerOptions{Concurrency: 1, BatchSize: 1, Poll: 10 * time.Millisecond, Shutdown: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-store.started
	started := time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("shutdown elapsed=%s", elapsed)
	}
}

func testScheduler(store Store, options RunnerOptions) *Runner {
	options.RequestTimeout = 100 * time.Millisecond
	options.LeaseTTL = 20 * time.Second
	options.WorkspaceLimit, options.EndpointLimit = 1, 1
	options.Retry = DefaultRetryPolicy()
	return NewRunnerWithOptions(store, cryptobox.Materials{}, config.ProfileTest, false, uuid.New(), options)
}

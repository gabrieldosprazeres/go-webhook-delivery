package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerProcessSIGTERM(t *testing.T) {
	if os.Getenv("WDE_WORKER_SIGNAL_HELPER") == "1" {
		if err := run(context.Background(), nil); err != nil {
			os.Exit(2)
		}
		return
	}
	databaseURL := os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("worker integration database URL is not configured")
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWorkerProcessSIGTERM$")
	command.Env = append(os.Environ(),
		"WDE_WORKER_SIGNAL_HELPER=1", "WDE_PROFILE=test",
		"WDE_DATABASE_URL="+databaseURL, "WDE_WORKER_OPERATIONAL_ADDR=127.0.0.1:0",
		"WDE_WORKER_POLL_INTERVAL=20ms", "WDE_WORKER_CLAIM_TIMEOUT=20ms")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	waitForWorkerStart(t, command, lines, &stderr)
	started := time.Now()
	if err = command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("worker exit: %v stderr=%s", err, stderr.String())
		}
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("worker did not stop after SIGTERM")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("worker shutdown elapsed=%s", elapsed)
	}
}

func waitForWorkerStart(t *testing.T, command *exec.Cmd, lines <-chan string, stderr *bytes.Buffer) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				_ = command.Wait()
				t.Fatalf("worker exited before startup: %s", stderr.String())
			}
			if strings.Contains(line, "service started") {
				return
			}
		case <-timeout.C:
			_ = command.Process.Kill()
			t.Fatal("worker startup timed out")
		}
	}
}

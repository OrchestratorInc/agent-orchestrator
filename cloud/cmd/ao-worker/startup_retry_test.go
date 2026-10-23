package main

import (
	"context"
	"testing"
	"time"
)

// A session-open request made while the checkout or Setup was still running
// (the desktop opening a new session) must not re-run the step the moment it
// fails: the prod Freestyle run re-ran a failed Setup 0.7s after it failed,
// with no Retry and no reopen. Only a request made after the failure is
// reported retries it.
func TestAwaitStartupRetryDropsARequestMadeBeforeTheFailure(t *testing.T) {
	t.Parallel()
	requests := make(chan struct{}, 1)
	requests <- struct{}{} // opened while Setup was running
	reported := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		done <- awaitStartupRetry(context.Background(), requests, func() { close(reported) })
	}()
	<-reported
	select {
	case retried := <-done:
		t.Fatalf("awaitStartupRetry returned %v on the request made before the failure", retried)
	case <-time.After(100 * time.Millisecond):
	}
	requests <- struct{}{} // Retry, or the failed session reopened
	select {
	case retried := <-done:
		if !retried {
			t.Fatal("awaitStartupRetry = false on a retry request, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("awaitStartupRetry did not return on a retry request made after the failure")
	}
}

func TestAwaitStartupRetryReportsBeforeWaiting(t *testing.T) {
	t.Parallel()
	requests := make(chan struct{}, 1)
	order := make(chan string, 2)
	done := make(chan bool, 1)
	go func() {
		done <- awaitStartupRetry(context.Background(), requests, func() { order <- "reported" })
	}()
	if got := <-order; got != "reported" {
		t.Fatalf("first = %q, want reported", got)
	}
	requests <- struct{}{}
	if !<-done {
		t.Fatal("awaitStartupRetry = false, want true")
	}
}

func TestAwaitStartupRetryStopsWithTheWorker(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	done := make(chan bool, 1)
	go func() { done <- awaitStartupRetry(ctx, requests, func() {}) }()
	cancel()
	select {
	case retried := <-done:
		if retried {
			t.Fatal("awaitStartupRetry = true after the worker stopped, want false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("awaitStartupRetry did not return when the worker stopped")
	}
}

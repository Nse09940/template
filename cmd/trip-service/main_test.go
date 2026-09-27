package main

import (
	"context"
	"errors"
	"testing"
)

func TestCloseWithinDeadlineWaitsForClose(t *testing.T) {
	t.Parallel()

	called := false
	err := closeWithinDeadline(context.Background(), func() {
		called = true
	})
	if err != nil {
		t.Fatalf("closeWithinDeadline: %v", err)
	}
	if !called {
		t.Fatal("close function was not called")
	}
}

func TestCloseWithinDeadlineStopsAtContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		result <- closeWithinDeadline(ctx, func() {
			close(started)
			<-release
		})
	}()

	<-started
	cancel()
	err := <-result
	close(release)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

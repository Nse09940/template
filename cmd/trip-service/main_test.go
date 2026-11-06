package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
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

func TestCloseWithinDeadlineStartsCloseAfterDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := make(chan struct{})

	err := closeWithinDeadline(ctx, func() {
		close(called)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("close function was not called")
	}
}

func TestJSONLoggerIncludesService(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	newJSONLogger(&output, slog.LevelInfo).Error("startup failed")

	var entry map[string]any
	if err := json.NewDecoder(&output).Decode(&entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if entry["service"] != serviceName {
		t.Fatalf("service = %v, want %q", entry["service"], serviceName)
	}
}

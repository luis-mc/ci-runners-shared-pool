//go:build !windows

package tray

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestWatchQuitCallsQuitOnSIGTERM(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	WatchQuit(ctx, func() { close(done) })
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("quit was not called after SIGTERM")
	}
}

func TestWatchQuitStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	WatchQuit(ctx, func() { called <- struct{}{} })
	cancel()
	select {
	case <-called:
		t.Fatal("quit called without a request")
	case <-time.After(200 * time.Millisecond):
	}
}

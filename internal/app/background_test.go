package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBackgroundRuntimeDrainsBeforeCancelingWork(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	runtime := StartBackgroundRuntime(nil, BackgroundTask{
		Name: "worker",
		Run: func(stopCtx, workCtx context.Context) error {
			close(started)
			<-stopCtx.Done()
			select {
			case <-finish:
				return nil
			case <-workCtx.Done():
				return workCtx.Err()
			}
		},
	})
	<-started

	runtime.BeginDrain()
	shortContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := runtime.Wait(shortContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, want deadline while in-flight work drains", err)
	}

	close(finish)
	waitContext, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := runtime.Wait(waitContext); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestBackgroundRuntimeForceStopCancelsWork(t *testing.T) {
	started := make(chan struct{})
	runtime := StartBackgroundRuntime(nil, BackgroundTask{
		Name: "worker",
		Run: func(_ context.Context, workCtx context.Context) error {
			close(started)
			<-workCtx.Done()
			return workCtx.Err()
		},
	})
	<-started

	runtime.ForceStop()
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Wait(waitContext); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestBackgroundRuntimeReportsPanic(t *testing.T) {
	runtime := StartBackgroundRuntime(nil, BackgroundTask{
		Name: "consumer",
		Run: func(context.Context, context.Context) error {
			panic("broken invariant")
		},
	})

	select {
	case err := <-runtime.Errors():
		if err == nil || !strings.Contains(err.Error(), "consumer") ||
			!strings.Contains(err.Error(), "broken invariant") {
			t.Fatalf("Errors() = %v, want component and panic", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background failure")
	}
}

func TestBackgroundRuntimeReportsUnexpectedReturn(t *testing.T) {
	runtime := StartBackgroundRuntime(nil, BackgroundTask{
		Name: "consumer",
		Run:  func(context.Context, context.Context) error { return nil },
	})

	select {
	case err := <-runtime.Errors():
		if err == nil || !strings.Contains(err.Error(), "returned before shutdown") {
			t.Fatalf("Errors() = %v, want unexpected return", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background failure")
	}
}

package configversion

import (
	"context"
	"testing"
	"time"
)

func TestWorkerStartAndStopAreIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewWorker(nil, "worker-test", nil)
	worker.Start(ctx)
	worker.Start(ctx)
	cancel()

	stopped := make(chan struct{})
	go func() {
		worker.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
	worker.Stop()
}

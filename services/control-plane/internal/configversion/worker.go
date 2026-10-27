package configversion

import (
	"context"
	"sync"
	"time"
)

type Worker struct {
	svc      *Service
	workerID string
	lease    time.Duration
	interval time.Duration
	batch    int

	stop chan struct{}
	wg   sync.WaitGroup
}

func NewWorker(svc *Service, workerID string) *Worker {
	if workerID == "" {
		workerID = "config-apply-worker"
	}
	return &Worker{
		svc:      svc,
		workerID: workerID,
		lease:    30 * time.Second,
		interval: 2 * time.Second,
		batch:    5,
		stop:     make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case <-ticker.C:
				runCtx, cancel := context.WithTimeout(ctx, w.lease)
				_, _ = w.svc.ProcessOnce(runCtx, w.workerID, w.lease, w.batch)
				cancel()
			}
		}
	}()
}

func (w *Worker) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	w.wg.Wait()
}

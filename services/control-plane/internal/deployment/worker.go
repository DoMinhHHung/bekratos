package deployment

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Worker struct {
	svc       *Service
	workerID  string
	lease     time.Duration
	interval  time.Duration
	batch     int
	log       *slog.Logger
	startOnce sync.Once
	stopOnce  sync.Once
	mu        sync.Mutex
	runCancel context.CancelFunc
	stop      chan struct{}
	wg        sync.WaitGroup
}

func NewWorker(svc *Service, workerID string, log *slog.Logger) *Worker {
	if workerID == "" {
		workerID = "deployment-worker"
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		svc:      svc,
		workerID: workerID,
		lease:    30 * time.Second,
		interval: 2 * time.Second,
		batch:    5,
		log:      log,
		stop:     make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		w.mu.Lock()
		w.runCancel = cancel
		w.mu.Unlock()
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.log.Info("deployment worker started", "worker_id", w.workerID)
			ticker := time.NewTicker(w.interval)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					w.log.Info("deployment worker stopping", "reason", "context_done", "worker_id", w.workerID)
					return
				case <-w.stop:
					w.log.Info("deployment worker stopping", "reason", "stop", "worker_id", w.workerID)
					return
				case <-ticker.C:
					n, err := w.svc.ProcessOnce(runCtx, w.workerID, w.lease, w.batch)
					if err != nil {
						w.log.Warn("deployment process cycle failed", "worker_id", w.workerID, "error_category", "claim_or_cycle_failed")
						continue
					}
					if n > 0 {
						w.log.Info("deployment process cycle", "worker_id", w.workerID, "processed", n)
					}
				}
			}
		}()
	})
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stop)
	})
	w.mu.Lock()
	cancel := w.runCancel
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

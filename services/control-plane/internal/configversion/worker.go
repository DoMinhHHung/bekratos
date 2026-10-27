package configversion

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Worker struct {
	svc      *Service
	workerID string
	lease    time.Duration
	interval time.Duration
	batch    int
	log      *slog.Logger

	startOnce sync.Once
	stop      chan struct{}
	wg        sync.WaitGroup
}

func NewWorker(svc *Service, workerID string, log *slog.Logger) *Worker {
	if workerID == "" {
		workerID = "config-apply-worker"
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
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.log.Info("config apply worker started", "worker_id", w.workerID)
			ticker := time.NewTicker(w.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					w.log.Info("config apply worker stopping", "reason", "context_done", "worker_id", w.workerID)
					return
				case <-w.stop:
					w.log.Info("config apply worker stopping", "reason", "stop", "worker_id", w.workerID)
					return
				case <-ticker.C:
					runCtx, cancel := context.WithTimeout(ctx, w.lease)
					n, err := w.svc.ProcessOnce(runCtx, w.workerID, w.lease, w.batch)
					cancel()
					if err != nil {
						w.log.Warn("config apply process cycle failed", "worker_id", w.workerID, "error", err.Error())
						continue
					}
					if n > 0 {
						w.log.Info("config apply process cycle", "worker_id", w.workerID, "processed", n)
					}
				}
			}
		}()
	})
}

func (w *Worker) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	w.wg.Wait()
}

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/config"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/configversion"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/module"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/project"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	httpServer *http.Server
	log        *slog.Logger
	worker     *configversion.Worker
	workerCtx  context.Context
	workerStop context.CancelFunc
	startOnce  sync.Once
}

func New(cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger, authMW *auth.Middleware) *Server {
	h := NewHandlers(pool, log, cfg.ReadinessTimeout)
	orgRepo := org.NewRepository(pool)
	orgSvc := org.NewService(orgRepo)
	orgHandlers := org.NewHandlers(orgSvc)

	projectRepo := project.NewRepository(pool)
	projectSvc := project.NewService(projectRepo, orgRepo)
	projectHandlers := project.NewHandlers(projectSvc)

	moduleRepo := module.NewRepository(pool)
	moduleSvc := module.NewService(moduleRepo)
	moduleHandlers := module.NewHandlers(moduleSvc)

	schemaRepo := schema.NewRepository(pool)
	schemaSvc := schema.NewService(schemaRepo, orgRepo, projectRepo)
	schemaHandlers := schema.NewHandlers(schemaSvc)

	configVersionRepo := configversion.NewRepository(pool)
	configVersionSvc := configversion.NewService(configVersionRepo, orgRepo, configversion.NewUnavailableApplyAdapter(), log)
	configVersionHandlers := configversion.NewHandlers(configVersionSvc)
	worker := configversion.NewWorker(configVersionSvc, "control-plane-apply-worker", log)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.Healthz)
	mux.HandleFunc("/readyz", h.Readyz)
	orgHandlers.Register(mux, authMW)
	projectHandlers.Register(mux, authMW)
	moduleHandlers.Register(mux, authMW)
	schemaHandlers.Register(mux, authMW)
	configVersionHandlers.Register(mux, authMW)

	var handler http.Handler = mux
	handler = MaxBytes(cfg.BodyLimitBytes)(handler)
	handler = AccessLog(log)(handler)
	handler = Recover(log)(handler)
	handler = RequestID(handler)

	workerCtx, workerStop := context.WithCancel(context.Background())

	return &Server{
		httpServer: &http.Server{
			Addr:         cfg.Addr(),
			Handler:      handler,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
		log:        log,
		worker:     worker,
		workerCtx:  workerCtx,
		workerStop: workerStop,
	}
}

func (s *Server) Start() error {
	s.startOnce.Do(func() {
		s.worker.Start(s.workerCtx)
	})
	s.log.Info("server starting", "addr", s.httpServer.Addr)
	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.workerStop()
	s.worker.Stop()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Addr() string {
	return s.httpServer.Addr
}

func WaitForShutdown(ctx context.Context, timeout time.Duration, shutdown func(context.Context) error, log *slog.Logger) {
	<-ctx.Done()
	log.Info("shutdown signal received")
	shutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := shutdown(shutCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err.Error())
		return
	}
	log.Info("server stopped")
}

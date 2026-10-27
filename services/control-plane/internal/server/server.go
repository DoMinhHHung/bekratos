package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/apikey"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/config"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/configversion"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/deployment"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/module"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/project"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	httpServer   *http.Server
	log          *slog.Logger
	applyWorker  *configversion.Worker
	deployWorker *deployment.Worker
	workerCtx    context.Context
	workerStop   context.CancelFunc
	startOnce    sync.Once
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

	runtimeApplyAdapter := configversion.ApplyAdapter(configversion.NewUnavailableApplyAdapter())
	deploymentProvider := deployment.Provider(deployment.NewUnavailableProvider())
	if cfg.RuntimeAPIURL != "" && cfg.RuntimeAPIToken != "" {
		runtimeApplyAdapter = configversion.NewHTTPApplyAdapter(cfg.RuntimeAPIURL, cfg.RuntimeAPIToken, cfg.RuntimeHTTPTimeout)
		deploymentProvider = deployment.NewHTTPProvider(cfg.RuntimeAPIURL, cfg.RuntimeAPIToken, cfg.RuntimeHTTPTimeout)
		log.Info("Runtime integration enabled", "provider", "runtime_http")
	} else {
		log.Warn("Runtime integration disabled", "reason", "runtime API URL/token not configured")
	}

	configVersionRepo := configversion.NewRepository(pool)
	configVersionSvc := configversion.NewService(configVersionRepo, orgRepo, runtimeApplyAdapter, log)
	configVersionHandlers := configversion.NewHandlers(configVersionSvc)
	applyWorker := configversion.NewWorker(configVersionSvc, "control-plane-apply-worker", log)

	apiKeyRepo := apikey.NewRepository(pool)
	apiKeySvc := apikey.NewService(apiKeyRepo, orgRepo)
	apiKeyHandlers := apikey.NewHandlers(apiKeySvc)

	deployRepo := deployment.NewRepository(pool)
	deploySvc := deployment.NewService(deployRepo, deploymentProvider, log)
	deployHandlers := deployment.NewHandlers(deploySvc)
	deployWorker := deployment.NewWorker(deploySvc, "control-plane-deployment-worker", log)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.Healthz)
	mux.HandleFunc("/readyz", h.Readyz)
	orgHandlers.Register(mux, authMW)
	projectHandlers.Register(mux, authMW)
	moduleHandlers.Register(mux, authMW)
	schemaHandlers.Register(mux, authMW)
	configVersionHandlers.Register(mux, authMW)
	apiKeyHandlers.Register(mux, authMW)
	deployHandlers.Register(mux, authMW)

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
		log:          log,
		applyWorker:  applyWorker,
		deployWorker: deployWorker,
		workerCtx:    workerCtx,
		workerStop:   workerStop,
	}
}

func (s *Server) Start() error {
	s.startOnce.Do(func() {
		s.applyWorker.Start(s.workerCtx)
		s.deployWorker.Start(s.workerCtx)
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
	s.applyWorker.Stop()
	s.deployWorker.Stop()
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

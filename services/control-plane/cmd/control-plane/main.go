package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/config"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err.Error())
		os.Exit(1)
	}

	log := newLogger(cfg.LogLevel)
	log.Info("revocation model",
		"mode", "token-expiry-bounded",
		"max_revocation_delay", cfg.MaxRevocationDelay.String(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection failed", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.MigrateOnStartup {
		log.Info("running migrations")
		if err := db.Migrate(ctx, pool); err != nil {
			log.Error("migration failed", "error", err.Error())
			os.Exit(1)
		}
		log.Info("migrations applied")
	}

	jwks := auth.NewJWKSProvider(cfg.IdentityJWKSURL, cfg.JWKSCacheTTL, cfg.JWKSHTTPTimeout)
	validator := auth.NewValidator(jwks, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTLeeway, cfg.MaxRevocationDelay)
	identityProfile := auth.NewIdentityProfileVerifier(cfg.IdentityProfileURL, cfg.IdentityServiceToken, cfg.JWKSHTTPTimeout)
	authMW := auth.NewMiddlewareWithIdentity(validator, log, identityProfile)

	srv := server.New(cfg, pool, log, authMW)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	go server.WaitForShutdown(ctx, cfg.ShutdownTimeout, srv.Shutdown, log)

	select {
	case err := <-errCh:
		if err != nil {
			log.Error("server error", "error", err.Error())
			os.Exit(1)
		}
	case <-ctx.Done():
		if err := <-errCh; err != nil {
			log.Error("server error after shutdown", "error", err.Error())
			os.Exit(1)
		}
	}
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(handler)
}

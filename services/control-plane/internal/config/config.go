package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host             string
	Port             int
	DatabaseURL      string
	LogLevel         string
	BodyLimitBytes   int64
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	IdleTimeout      time.Duration
	ShutdownTimeout  time.Duration
	ReadinessTimeout time.Duration
	MigrateOnStartup bool
}

func Load() (*Config, error) {
	cfg := &Config{
		Host:             getEnv("HOST", "0.0.0.0"),
		Port:             8080,
		LogLevel:         strings.ToLower(getEnv("LOG_LEVEL", "info")),
		BodyLimitBytes:   1 << 20,
		ReadTimeout:      15 * time.Second,
		WriteTimeout:     15 * time.Second,
		IdleTimeout:      60 * time.Second,
		ShutdownTimeout:  15 * time.Second,
		ReadinessTimeout: 2 * time.Second,
		MigrateOnStartup: false,
	}

	if v := os.Getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("invalid PORT: %q", v)
		}
		cfg.Port = p
	}

	cfg.DatabaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if !strings.HasPrefix(cfg.DatabaseURL, "postgres://") && !strings.HasPrefix(cfg.DatabaseURL, "postgresql://") {
		return nil, fmt.Errorf("DATABASE_URL must be a postgres connection string")
	}

	if v := os.Getenv("BODY_LIMIT_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1024 || n > 64<<20 {
			return nil, fmt.Errorf("invalid BODY_LIMIT_BYTES: %q", v)
		}
		cfg.BodyLimitBytes = n
	}

	var err error
	if cfg.ReadTimeout, err = parseDurationEnv("READ_TIMEOUT", cfg.ReadTimeout); err != nil {
		return nil, err
	}
	if cfg.WriteTimeout, err = parseDurationEnv("WRITE_TIMEOUT", cfg.WriteTimeout); err != nil {
		return nil, err
	}
	if cfg.IdleTimeout, err = parseDurationEnv("IDLE_TIMEOUT", cfg.IdleTimeout); err != nil {
		return nil, err
	}
	if cfg.ShutdownTimeout, err = parseDurationEnv("SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return nil, err
	}
	if cfg.ReadinessTimeout, err = parseDurationEnv("READINESS_TIMEOUT", cfg.ReadinessTimeout); err != nil {
		return nil, err
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("invalid LOG_LEVEL: %q", cfg.LogLevel)
	}

	if v := os.Getenv("MIGRATE_ON_STARTUP"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid MIGRATE_ON_STARTUP: %q", v)
		}
		cfg.MigrateOnStartup = b
	}

	return cfg, nil
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 || d > 5*time.Minute {
		return 0, fmt.Errorf("invalid %s: %q", key, v)
	}
	return d, nil
}

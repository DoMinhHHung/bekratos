package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Host                string
	Port                int
	DatabaseURL         string
	LogLevel            string
	BodyLimitBytes      int64
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
	ReadinessTimeout    time.Duration
	MigrateOnStartup    bool
	IdentityJWKSURL     string
	IdentityProfileURL  string
	IdentityServiceToken string
	RuntimeAPIURL       string
	RuntimeServiceToken string
	RuntimeHTTPTimeout  time.Duration
	JWTIssuer           string
	JWTAudience         string
	JWKSCacheTTL        time.Duration
	JWKSHTTPTimeout     time.Duration
	JWTLeeway           time.Duration
	MaxRevocationDelay  time.Duration
}

func Load() (*Config, error) {
	if err := loadDotEnv(); err != nil {
		return nil, err
	}

	cfg := &Config{
		Host:               getEnv("HOST", "0.0.0.0"),
		Port:               8080,
		LogLevel:           strings.ToLower(getEnv("LOG_LEVEL", "info")),
		BodyLimitBytes:     1 << 20,
		ReadTimeout:        15 * time.Second,
		WriteTimeout:       15 * time.Second,
		IdleTimeout:        60 * time.Second,
		ShutdownTimeout:    15 * time.Second,
		ReadinessTimeout:   2 * time.Second,
		MigrateOnStartup:   false,
		JWKSCacheTTL:       60 * time.Second,
		JWKSHTTPTimeout:    3 * time.Second,
		JWTLeeway:          30 * time.Second,
		MaxRevocationDelay: 15 * time.Minute,
		RuntimeHTTPTimeout: 5 * time.Second,
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

	cfg.IdentityJWKSURL = strings.TrimSpace(os.Getenv("IDENTITY_JWKS_URL"))
	if cfg.IdentityJWKSURL == "" {
		return nil, fmt.Errorf("IDENTITY_JWKS_URL is required")
	}
	if err := validateJWKSURL(cfg.IdentityJWKSURL); err != nil {
		return nil, err
	}

	cfg.RuntimeAPIURL = strings.TrimSpace(os.Getenv("RUNTIME_API_URL"))
	cfg.RuntimeServiceToken = strings.TrimSpace(os.Getenv("RUNTIME_SERVICE_TOKEN"))
	if cfg.RuntimeAPIURL == "" && cfg.RuntimeServiceToken != "" {
		return nil, fmt.Errorf("RUNTIME_API_URL is required when RUNTIME_SERVICE_TOKEN is configured")
	}
	if cfg.RuntimeAPIURL != "" {
		if err := validateRuntimeURL(cfg.RuntimeAPIURL); err != nil {
			return nil, err
		}
		if len(cfg.RuntimeServiceToken) < 32 {
			return nil, fmt.Errorf("RUNTIME_SERVICE_TOKEN must contain at least 32 characters")
		}
	}

	cfg.IdentityServiceToken = strings.TrimSpace(os.Getenv("IDENTITY_SERVICE_TOKEN"))
	if len(cfg.IdentityServiceToken) < 32 {
		return nil, fmt.Errorf("IDENTITY_SERVICE_TOKEN must contain at least 32 characters")
	}
	cfg.IdentityProfileURL = strings.TrimSpace(os.Getenv("IDENTITY_PROFILE_URL"))
	if cfg.IdentityProfileURL == "" {
		identityURL, err := url.Parse(cfg.IdentityJWKSURL)
		if err != nil {
			return nil, fmt.Errorf("IDENTITY_PROFILE_URL could not be derived from IDENTITY_JWKS_URL")
		}
		identityURL.Path = "/v1/internal/session"
		identityURL.RawQuery = ""
		identityURL.Fragment = ""
		cfg.IdentityProfileURL = identityURL.String()
	}
	if err := validateJWKSURL(cfg.IdentityProfileURL); err != nil {
		return nil, fmt.Errorf("invalid IDENTITY_PROFILE_URL")
	}

	cfg.JWTIssuer = strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if cfg.JWTIssuer == "" {
		return nil, fmt.Errorf("JWT_ISSUER is required")
	}
	cfg.JWTAudience = strings.TrimSpace(os.Getenv("JWT_AUDIENCE"))
	if cfg.JWTAudience == "" {
		return nil, fmt.Errorf("JWT_AUDIENCE is required")
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
	if cfg.JWKSCacheTTL, err = parseDurationEnv("JWKS_CACHE_TTL", cfg.JWKSCacheTTL); err != nil {
		return nil, err
	}
	if cfg.JWKSHTTPTimeout, err = parseDurationEnv("JWKS_HTTP_TIMEOUT", cfg.JWKSHTTPTimeout); err != nil {
		return nil, err
	}
	if cfg.JWTLeeway, err = parseDurationEnv("JWT_LEEWAY", cfg.JWTLeeway); err != nil {
		return nil, err
	}
	if cfg.MaxRevocationDelay, err = parseDurationEnv("MAX_REVOCATION_DELAY", cfg.MaxRevocationDelay); err != nil {
		return nil, err
	}
	if cfg.RuntimeHTTPTimeout, err = parseDurationEnv("RUNTIME_HTTP_TIMEOUT", cfg.RuntimeHTTPTimeout); err != nil {
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

func loadDotEnv() error {
	if path := strings.TrimSpace(os.Getenv("ENV_FILE")); path != "" {
		if err := godotenv.Load(path); err != nil {
			return fmt.Errorf("failed to load ENV_FILE")
		}
		return nil
	}

	candidates := []string{".env"}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, ".env"),
			filepath.Join(wd, "services", "control-plane", ".env"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		if err := godotenv.Load(c); err != nil {
			return fmt.Errorf("failed to load dotenv file")
		}
		return nil
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func validateJWKSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("IDENTITY_JWKS_URL must be an absolute http(s) URL")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback {
			return nil
		}
		return fmt.Errorf("IDENTITY_JWKS_URL must use https except for loopback hosts")
	default:
		return fmt.Errorf("IDENTITY_JWKS_URL must be an absolute http(s) URL")
	}
}

func parseDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 || d > 24*time.Hour {
		return 0, fmt.Errorf("invalid %s: %q", key, v)
	}
	return d, nil
}

func validateRuntimeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("RUNTIME_API_URL must be an absolute http(s) URL without credentials, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback {
			return nil
		}
		return fmt.Errorf("RUNTIME_API_URL must use https except for loopback hosts")
	default:
		return fmt.Errorf("RUNTIME_API_URL must be an absolute http(s) URL")
	}
}

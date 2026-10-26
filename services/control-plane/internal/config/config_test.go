package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadRejectsNonPostgresURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "mysql://user:pass@localhost/db")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for non-postgres URL")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://bekratos:bekratos@localhost:5432/bekratos")
	t.Setenv("PORT", "")
	t.Setenv("HOST", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("BODY_LIMIT_BYTES", "")
	t.Setenv("MIGRATE_ON_STARTUP", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("host = %q, want 0.0.0.0", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("log level = %q, want info", cfg.LogLevel)
	}
	if cfg.BodyLimitBytes != 1<<20 {
		t.Errorf("body limit = %d, want %d", cfg.BodyLimitBytes, 1<<20)
	}
	if cfg.MigrateOnStartup {
		t.Error("MigrateOnStartup should default to false")
	}
	if cfg.ReadTimeout != 15*time.Second {
		t.Errorf("read timeout = %v", cfg.ReadTimeout)
	}
}

func TestLoadValidOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://u:p@db:5432/cp")
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("BODY_LIMIT_BYTES", "2097152")
	t.Setenv("READ_TIMEOUT", "5s")
	t.Setenv("MIGRATE_ON_STARTUP", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 9090 {
		t.Errorf("addr mismatch: %s", cfg.Addr())
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("log level = %q", cfg.LogLevel)
	}
	if cfg.BodyLimitBytes != 2097152 {
		t.Errorf("body limit = %d", cfg.BodyLimitBytes)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("read timeout = %v", cfg.ReadTimeout)
	}
	if !cfg.MigrateOnStartup {
		t.Error("expected MigrateOnStartup true")
	}
}

func TestLoadInvalidPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("PORT", "abc")
	_, err := Load()
	if err == nil {
		t.Fatal("expected invalid PORT error")
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("LOG_LEVEL", "trace")
	_, err := Load()
	if err == nil {
		t.Fatal("expected invalid LOG_LEVEL error")
	}
}

func TestLoadDoesNotExposeSecretsInError(t *testing.T) {
	secret := "postgres://user:supersecretpassword@localhost:5432/db"
	t.Setenv("DATABASE_URL", secret)
	t.Setenv("PORT", "not-a-number")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error")
	}
	if contains(err.Error(), "supersecretpassword") {
		t.Error("error message must not contain database password")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

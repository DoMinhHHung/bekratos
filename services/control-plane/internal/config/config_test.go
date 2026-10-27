package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://bekratos:bekratos@localhost:5432/bekratos")
	t.Setenv("IDENTITY_JWKS_URL", "http://localhost:8080/v1/jwks")
	t.Setenv("IDENTITY_INTROSPECTION_URL", "http://localhost:8080/internal/v1/identity/introspect")
	t.Setenv("IDENTITY_SERVICE_API_TOKEN", "synthetic-identity-service-token-0123456789")
	t.Setenv("JWT_ISSUER", "https://identity.bekratos.local")
	t.Setenv("JWT_AUDIENCE", "bekratos-api")
	t.Setenv("ENV_FILE", "")
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is empty")
	}
}

func TestLoadRequiresJWKSAndJWT(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("IDENTITY_JWKS_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected IDENTITY_JWKS_URL error")
	}
	setBaseEnv(t)
	t.Setenv("JWT_ISSUER", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected JWT_ISSUER error")
	}
}

func TestLoadDefaults(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("PORT", "")
	t.Setenv("HOST", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("MIGRATE_ON_STARTUP", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 8080 || cfg.LogLevel != "info" {
		t.Fatalf("defaults mismatch: %+v", cfg)
	}
	if cfg.MigrateOnStartup {
		t.Fatal("MigrateOnStartup should default false")
	}
	if cfg.JWKSCacheTTL != 60*time.Second {
		t.Fatalf("jwks ttl %v", cfg.JWKSCacheTTL)
	}
	if cfg.IdentityHTTPTimeout != 2*time.Second || cfg.RuntimeHTTPTimeout != 25*time.Second {
		t.Fatalf("service timeouts mismatch: identity=%v runtime=%v", cfg.IdentityHTTPTimeout, cfg.RuntimeHTTPTimeout)
	}
	if cfg.RuntimeAPIURL != "" || cfg.RuntimeAPIToken != "" {
		t.Fatal("runtime adapter should be disabled by default")
	}
	if cfg.MaxRevocationDelay != 15*time.Minute {
		t.Fatalf("max revocation delay %v", cfg.MaxRevocationDelay)
	}
}

func TestLoadAcceptsLongJWKSCacheTTLWithinConfigurationLimit(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWKS_CACHE_TTL", "24h")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("long JWKS_CACHE_TTL should remain accepted: %v", err)
	}
	if cfg.JWKSCacheTTL != 24*time.Hour {
		t.Fatalf("JWKSCacheTTL = %v, want 24h", cfg.JWKSCacheTTL)
	}
}

func TestLoadENVFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	content := "DATABASE_URL=postgres://fromfile@localhost/db\nIDENTITY_JWKS_URL=https://jwks.example/v1/jwks\nJWT_ISSUER=https://iss.example\nJWT_AUDIENCE=aud-example\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV_FILE", path)
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("IDENTITY_JWKS_URL")
	os.Unsetenv("IDENTITY_INTROSPECTION_URL")
	os.Unsetenv("IDENTITY_SERVICE_API_TOKEN")
	os.Unsetenv("JWT_ISSUER")
	os.Unsetenv("JWT_AUDIENCE")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://fromfile@localhost/db" {
		t.Fatalf("url=%q", cfg.DatabaseURL)
	}
	if cfg.IdentityJWKSURL != "https://jwks.example/v1/jwks" {
		t.Fatalf("jwks=%q", cfg.IdentityJWKSURL)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	content := "DATABASE_URL=postgres://fromfile@localhost/db\nIDENTITY_JWKS_URL=https://jwks.example/v1/jwks\nJWT_ISSUER=https://iss.example\nJWT_AUDIENCE=aud-example\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV_FILE", path)
	t.Setenv("DATABASE_URL", "postgres://fromenv@localhost/db")
	t.Setenv("IDENTITY_JWKS_URL", "https://jwks.env.example/v1/jwks")
	t.Setenv("IDENTITY_INTROSPECTION_URL", "https://identity.env.example/internal/v1/identity/introspect")
	t.Setenv("IDENTITY_SERVICE_API_TOKEN", "synthetic-identity-service-token-env-0123456789")
	t.Setenv("JWT_ISSUER", "https://iss.example")
	t.Setenv("JWT_AUDIENCE", "aud-example")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://fromenv@localhost/db" {
		t.Fatalf("expected env override, got %q", cfg.DatabaseURL)
	}
	if cfg.IdentityJWKSURL != "https://jwks.env.example/v1/jwks" {
		t.Fatalf("jwks=%q", cfg.IdentityJWKSURL)
	}
}

func TestLoadInvalidENVFileFailsFast(t *testing.T) {
	t.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("IDENTITY_JWKS_URL", "https://example.com/v1/jwks")
	t.Setenv("IDENTITY_INTROSPECTION_URL", "https://example.com/internal/v1/identity/introspect")
	t.Setenv("IDENTITY_SERVICE_API_TOKEN", "synthetic-identity-service-token-0123456789")
	t.Setenv("JWT_ISSUER", "iss")
	t.Setenv("JWT_AUDIENCE", "aud")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing ENV_FILE")
	}
	if contains(err.Error(), "password") || contains(err.Error(), "secret") {
		t.Fatalf("error leaked content: %v", err)
	}
}

func TestRejectNonLoopbackHTTPJWKS(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("IDENTITY_JWKS_URL", "http://jwks.example/v1/jwks")
	_, err := Load()
	if err == nil {
		t.Fatal("expected rejection of non-loopback http JWKS URL")
	}
}

func TestAcceptLoopbackHTTPJWKS(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("IDENTITY_JWKS_URL", "http://127.0.0.1:8080/v1/jwks")
	if _, err := Load(); err != nil {
		t.Fatalf("loopback http should be accepted: %v", err)
	}
}

func TestRejectMissingIdentityServiceToken(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("IDENTITY_SERVICE_API_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected Identity service token to be required")
	}
}

func TestRuntimeSettingsMustBeConfiguredTogether(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("RUNTIME_API_URL", "https://runtime.example")
	t.Setenv("RUNTIME_API_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected incomplete Runtime configuration to fail")
	}
}

func TestRejectNonLoopbackHTTPRuntimeURL(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("RUNTIME_API_URL", "http://runtime.example")
	t.Setenv("RUNTIME_API_TOKEN", "synthetic-runtime-api-token-0123456789")
	if _, err := Load(); err == nil {
		t.Fatal("expected non-loopback HTTP Runtime URL to fail")
	}
}

func TestAcceptHTTPSJWKS(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("IDENTITY_JWKS_URL", "https://identity.example/v1/jwks")
	if _, err := Load(); err != nil {
		t.Fatalf("https should be accepted: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

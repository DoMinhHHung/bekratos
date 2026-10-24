use std::env;
use std::net::SocketAddr;
use std::time::Duration;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone)]
pub struct Config {
    pub host: String,
    pub port: u16,
    pub bind_addr: SocketAddr,
    pub database_url: String,
    pub redis_url: String,
    pub request_timeout: Duration,
    pub body_limit_bytes: usize,
    pub readiness_require_db: bool,
    pub readiness_require_redis: bool,
    pub magic_link_hmac_keys: String,
    pub magic_link_active_kid: String,
    pub app_public_base_url: String,
    pub mail_provider: String,
    pub mail_from: String,
    pub argon2_memory_kib: u32,
    pub argon2_iterations: u32,
    pub argon2_parallelism: u32,
    pub jwt_signing_key_pem: String,
    pub jwt_kid: String,
    pub jwt_issuer: String,
    pub jwt_audience: String,
    pub jwt_access_ttl_secs: i64,
    pub jwt_key_encryption_key: String,
    pub jwks_cache_ttl_secs: u64,
    pub refresh_ttl_secs: i64,
    pub session_ttl_secs: i64,
}

impl Config {
    pub fn from_env() -> AppResult<Self> {
        let _ = dotenvy::dotenv();

        let host = env::var("HOST").unwrap_or_else(|_| "0.0.0.0".to_string());
        let port: u16 = env::var("PORT")
            .unwrap_or_else(|_| "8080".to_string())
            .parse()
            .map_err(|_| AppError::config("PORT must be a valid u16"))?;

        let bind_addr: SocketAddr = format!("{host}:{port}")
            .parse()
            .map_err(|_| AppError::config("HOST:PORT is not a valid socket address"))?;

        let database_url =
            env::var("DATABASE_URL").map_err(|_| AppError::config("DATABASE_URL is required"))?;
        if database_url.is_empty() {
            return Err(AppError::config("DATABASE_URL must not be empty"));
        }

        let redis_url =
            env::var("REDIS_URL").map_err(|_| AppError::config("REDIS_URL is required"))?;
        if redis_url.is_empty() {
            return Err(AppError::config("REDIS_URL must not be empty"));
        }

        let request_timeout_secs: u64 = env::var("REQUEST_TIMEOUT_SECS")
            .unwrap_or_else(|_| "30".to_string())
            .parse()
            .map_err(|_| AppError::config("REQUEST_TIMEOUT_SECS must be a valid u64"))?;
        if request_timeout_secs == 0 {
            return Err(AppError::config("REQUEST_TIMEOUT_SECS must be > 0"));
        }

        let body_limit_bytes: usize = env::var("BODY_LIMIT_BYTES")
            .unwrap_or_else(|_| "1048576".to_string())
            .parse()
            .map_err(|_| AppError::config("BODY_LIMIT_BYTES must be a valid usize"))?;
        if body_limit_bytes == 0 {
            return Err(AppError::config("BODY_LIMIT_BYTES must be > 0"));
        }

        let readiness_require_db = parse_bool_env("READINESS_REQUIRE_DB", true)?;
        let readiness_require_redis = parse_bool_env("READINESS_REQUIRE_REDIS", true)?;

        let magic_link_hmac_keys = env::var("MAGIC_LINK_HMAC_KEYS")
            .map_err(|_| AppError::config("MAGIC_LINK_HMAC_KEYS is required"))?;
        let magic_link_active_kid = env::var("MAGIC_LINK_ACTIVE_KID")
            .map_err(|_| AppError::config("MAGIC_LINK_ACTIVE_KID is required"))?;
        let app_public_base_url = env::var("APP_PUBLIC_BASE_URL")
            .map_err(|_| AppError::config("APP_PUBLIC_BASE_URL is required"))?;
        if app_public_base_url.is_empty() {
            return Err(AppError::config("APP_PUBLIC_BASE_URL must not be empty"));
        }
        let mail_provider = env::var("MAIL_PROVIDER").unwrap_or_else(|_| "console".to_string());
        let mail_from = env::var("MAIL_FROM").unwrap_or_else(|_| "noreply@localhost".to_string());

        let argon2_memory_kib: u32 = env::var("ARGON2_MEMORY_KIB")
            .unwrap_or_else(|_| "19456".to_string())
            .parse()
            .map_err(|_| AppError::config("ARGON2_MEMORY_KIB must be u32"))?;
        let argon2_iterations: u32 = env::var("ARGON2_ITERATIONS")
            .unwrap_or_else(|_| "2".to_string())
            .parse()
            .map_err(|_| AppError::config("ARGON2_ITERATIONS must be u32"))?;
        let argon2_parallelism: u32 = env::var("ARGON2_PARALLELISM")
            .unwrap_or_else(|_| "1".to_string())
            .parse()
            .map_err(|_| AppError::config("ARGON2_PARALLELISM must be u32"))?;
        let jwt_signing_key_pem = env::var("JWT_SIGNING_KEY_PEM")
            .map_err(|_| AppError::config("JWT_SIGNING_KEY_PEM is required"))?;
        let jwt_kid = env::var("JWT_KID").unwrap_or_else(|_| "es256-1".to_string());
        let jwt_issuer =
            env::var("JWT_ISSUER").map_err(|_| AppError::config("JWT_ISSUER is required"))?;
        let jwt_audience =
            env::var("JWT_AUDIENCE").map_err(|_| AppError::config("JWT_AUDIENCE is required"))?;
        let jwt_access_ttl_secs: i64 = env::var("JWT_ACCESS_TTL_SECS")
            .unwrap_or_else(|_| "900".to_string())
            .parse()
            .map_err(|_| AppError::config("JWT_ACCESS_TTL_SECS must be i64"))?;
        let refresh_ttl_secs: i64 = env::var("REFRESH_TTL_SECS")
            .unwrap_or_else(|_| "2592000".to_string())
            .parse()
            .map_err(|_| AppError::config("REFRESH_TTL_SECS must be i64"))?;
        let session_ttl_secs: i64 = env::var("SESSION_TTL_SECS")
            .unwrap_or_else(|_| "2592000".to_string())
            .parse()
            .map_err(|_| AppError::config("SESSION_TTL_SECS must be i64"))?;
        if jwt_access_ttl_secs <= 0 || refresh_ttl_secs <= 0 || session_ttl_secs <= 0 {
            return Err(AppError::config("token/session TTLs must be > 0"));
        }
        let jwt_key_encryption_key = env::var("JWT_KEY_ENCRYPTION_KEY")
            .map_err(|_| AppError::config("JWT_KEY_ENCRYPTION_KEY is required"))?;
        if jwt_key_encryption_key.len() < 32 {
            return Err(AppError::config(
                "JWT_KEY_ENCRYPTION_KEY must be at least 32 characters",
            ));
        }
        let jwks_cache_ttl_secs: u64 = env::var("JWKS_CACHE_TTL_SECS")
            .unwrap_or_else(|_| "60".to_string())
            .parse()
            .map_err(|_| AppError::config("JWKS_CACHE_TTL_SECS must be u64"))?;

        Ok(Self {
            host,
            port,
            bind_addr,
            database_url,
            redis_url,
            request_timeout: Duration::from_secs(request_timeout_secs),
            body_limit_bytes,
            readiness_require_db,
            readiness_require_redis,
            magic_link_hmac_keys,
            magic_link_active_kid,
            app_public_base_url,
            mail_provider,
            mail_from,
            argon2_memory_kib,
            argon2_iterations,
            argon2_parallelism,
            jwt_signing_key_pem,
            jwt_kid,
            jwt_issuer,
            jwt_audience,
            jwt_access_ttl_secs,
            refresh_ttl_secs,
            session_ttl_secs,
            jwt_key_encryption_key,
            jwks_cache_ttl_secs,
        })
    }
}

fn parse_bool_env(key: &str, default: bool) -> AppResult<bool> {
    match env::var(key) {
        Ok(v) => match v.to_ascii_lowercase().as_str() {
            "1" | "true" | "yes" | "on" => Ok(true),
            "0" | "false" | "no" | "off" => Ok(false),
            _ => Err(AppError::config(format!(
                "{key} must be a boolean (true/false)"
            ))),
        },
        Err(_) => Ok(default),
    }
}

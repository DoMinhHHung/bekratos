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

        let database_url = env::var("DATABASE_URL")
            .map_err(|_| AppError::config("DATABASE_URL is required"))?;
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

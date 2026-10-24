use std::sync::Arc;

use redis::aio::ConnectionManager;
use sqlx::PgPool;

use crate::config::Config;
use crate::infra::password::PasswordPolicy;
use crate::middleware::rate_limit::RateLimiter;

#[derive(Clone)]
pub struct AppState {
    pub config: Arc<Config>,
    pub db: PgPool,
    pub redis: ConnectionManager,
    pub rate_limiter: RateLimiter,
    pub password_policy: PasswordPolicy,
}

impl AppState {
    pub fn new(
        config: Config,
        db: PgPool,
        redis: ConnectionManager,
        password_policy: PasswordPolicy,
    ) -> Self {
        let rate_limiter = RateLimiter::new(redis.clone());
        Self {
            config: Arc::new(config),
            db,
            redis,
            rate_limiter,
            password_policy,
        }
    }
}

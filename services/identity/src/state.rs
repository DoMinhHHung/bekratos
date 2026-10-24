use std::sync::Arc;

use redis::aio::ConnectionManager;
use sqlx::PgPool;
use tokio::sync::Semaphore;

use crate::config::Config;
use crate::infra::jwt::{JwtSigner, JwtVerifier};
use crate::infra::password::PasswordPolicy;
use crate::infra::tokens::MagicLinkSigner;
use crate::middleware::rate_limit::RateLimiter;

#[derive(Clone)]
pub struct AppState {
    pub config: Arc<Config>,
    pub db: PgPool,
    pub redis: ConnectionManager,
    pub rate_limiter: RateLimiter,
    pub password_policy: PasswordPolicy,
    pub magic_link_signer: MagicLinkSigner,
    pub jwt_signer: JwtSigner,
    pub jwt_verifier: JwtVerifier,
    pub argon_permits: Arc<Semaphore>,
}

impl AppState {
    /// Tập hợp các phụ thuộc dùng chung và khởi tạo giới hạn số tác vụ Argon2 đồng thời
    /// theo `argon2_max_concurrent`; dùng kết nối Redis đã cấp cho bộ giới hạn yêu cầu.
    pub fn new(
        config: Config,
        db: PgPool,
        redis: ConnectionManager,
        password_policy: PasswordPolicy,
        magic_link_signer: MagicLinkSigner,
        jwt_signer: JwtSigner,
        jwt_verifier: JwtVerifier,
    ) -> Self {
        let rate_limiter = RateLimiter::new(redis.clone());
        let argon_permits = Arc::new(Semaphore::new(config.argon2_max_concurrent));
        Self {
            config: Arc::new(config),
            db,
            redis,
            rate_limiter,
            password_policy,
            magic_link_signer,
            jwt_signer,
            jwt_verifier,
            argon_permits,
        }
    }
}
